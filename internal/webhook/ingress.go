// Package webhook provides both the outbound delivery engine (dispatcher.go,
// signer.go) and the inbound handler for events sent by OpenPay (this file).
package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"

	"github.com/rs/zerolog"

	"github.com/menesesghz/openpay-smart-service/internal/domain"
	"github.com/menesesghz/openpay-smart-service/internal/openpay"
	"github.com/menesesghz/openpay-smart-service/internal/repository"
)

// EventPublisher publishes domain payment events to a message bus (Kafka).
// The ingress handler calls this after settling a payment so that:
//   - The outbound webhook dispatcher forwards the event to the tenant's endpoint
//   - The gRPC StreamPaymentEvents RPC pushes it to connected subscribers
type EventPublisher interface {
	PublishPaymentEvent(ctx context.Context, evt domain.PaymentEvent) error
}

// IngressHandler is the HTTP handler for POST /webhooks/openpay.
//
// OpenPay sends events to this endpoint whenever a transaction changes state.
// The handler:
//  1. Reads and size-limits the request body
//  2. Verifies the shared secret configured in the OpenPay dashboard
//     (passed as HTTP Basic Auth password; username is ignored)
//  3. Responds to the initial endpoint verification challenge
//  4. Dispatches to the appropriate event handler based on event.Type
//
// Registration in main.go:
//
//	mux.Handle("/webhooks/openpay", ingressHandler)
type IngressHandler struct {
	secret    string // OpenPay webhook shared secret (cfg.OpenPay.WebhookIngressSecret)
	payments  repository.PaymentRepository
	tenants   repository.TenantRepository
	balances  repository.BalanceRepository
	publisher EventPublisher
	log       zerolog.Logger
}

// NewIngressHandler constructs an IngressHandler.
// secret must match the value configured in the OpenPay merchant dashboard
// under Configuración → Notificaciones → Clave de verificación.
func NewIngressHandler(
	secret string,
	payments repository.PaymentRepository,
	tenants repository.TenantRepository,
	balances repository.BalanceRepository,
	publisher EventPublisher,
	log zerolog.Logger,
) *IngressHandler {
	return &IngressHandler{
		secret:    secret,
		payments:  payments,
		tenants:   tenants,
		balances:  balances,
		publisher: publisher,
		log:       log.With().Str("component", "webhook_ingress").Logger(),
	}
}

const (
	maxBodyBytes          = 1 << 20 // 1 MiB — OpenPay payloads are tiny; guard against abuse
	eventTypeVerification = "verification"
)

// ServeHTTP implements http.Handler.
func (h *IngressHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// ── 1. Verify shared secret via HTTP Basic Auth ───────────────────────────
	// In OpenPay dashboard, set "Usuario" and "Contraseña" for the webhook URL.
	// We only check the password; OpenPay allows any username.
	if h.secret != "" {
		_, pass, ok := r.BasicAuth()
		if !ok || pass != h.secret {
			h.log.Warn().Msg("webhook ingress: unauthorized request (bad or missing Basic Auth)")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	// ── 2. Read body ─────────────────────────────────────────────────────────
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		h.log.Error().Err(err).Msg("webhook ingress: failed to read body")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// ── 3. Parse the OpenPay event envelope ───────────────────────────────────
	var event domain.OpenPayEvent
	if err := json.Unmarshal(body, &event); err != nil {
		h.log.Error().Err(err).Msg("webhook ingress: failed to parse event JSON")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	log := h.log.With().Str("event_type", event.Type).Logger()

	// ── 4. Dispatch ───────────────────────────────────────────────────────────
	ctx := r.Context()
	var handlerErr error

	switch event.Type {

	case eventTypeVerification:
		// OpenPay sends this once when you first register the webhook URL.
		// Respond with HTTP 200 and the verification_code in the body.
		// After this the endpoint is marked "active" in the dashboard.
		log.Info().Str("code", event.VerificationCode).Msg("webhook endpoint verification")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"verification_code": event.VerificationCode,
		})
		return

	case "charge.succeeded":
		handlerErr = h.handleChargeSucceeded(ctx, event, log)

	case "charge.failed":
		handlerErr = h.handleChargeStatus(ctx, event, domain.PaymentStatusFailed, log)

	case "charge.cancelled":
		handlerErr = h.handleChargeStatus(ctx, event, domain.PaymentStatusCancelled, log)

	case "charge.refunded":
		handlerErr = h.handleChargeStatus(ctx, event, domain.PaymentStatusRefunded, log)

	case "chargeback.accepted":
		handlerErr = h.handleChargeStatus(ctx, event, domain.PaymentStatusChargeback, log)

	default:
		// Unknown event type — log and ack (return 200) so OpenPay doesn't retry.
		log.Debug().Msg("webhook ingress: unhandled event type, ignoring")
	}

	if handlerErr != nil {
		// Return 500 so OpenPay will retry the delivery.
		log.Error().Err(handlerErr).Msg("webhook ingress: handler error")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// ─── charge.succeeded ────────────────────────────────────────────────────────

// handleChargeSucceeded is the most important handler in the service.
// It:
//  1. Parses the charge from the event payload
//  2. Looks up the internal Payment record by openpay_transaction_id
//  3. Fetches the tenant to get their PlatformFeeBPS
//  4. Computes the three-way fee split using domain.NetAmountForCharge
//  5. Atomically settles the payment (writes all 4 fee fields + status=completed)
//  6. Atomically credits the tenant balance (pending → available, net_amount only)
//  7. Publishes a PaymentEvent to Kafka for downstream consumers
func (h *IngressHandler) handleChargeSucceeded(ctx context.Context, event domain.OpenPayEvent, log zerolog.Logger) error {
	charge, err := parseCharge(event)
	if err != nil {
		return fmt.Errorf("parse charge: %w", err)
	}

	log = log.With().Str("openpay_tx_id", charge.ID).Logger()

	// Look up our internal payment record.
	payment, err := h.payments.GetByOpenpayTransactionID(ctx, charge.ID)
	if err != nil {
		return fmt.Errorf("get payment by openpay tx %s: %w", charge.ID, err)
	}

	// Fetch tenant to get PlatformFeeBPS.
	tenant, err := h.tenants.GetByID(ctx, payment.TenantID)
	if err != nil {
		return fmt.Errorf("get tenant %s: %w", payment.TenantID, err)
	}

	// Convert OpenPay amounts (float64 MXN) → centavos (int64).
	// OpenPay's fee_details.Amount is the gross fee in pesos; multiply by 100.
	grossCentavos := floatToCentavos(charge.Amount)

	var openpayFeeCentavos int64
	if charge.FeeDetails != nil {
		openpayFeeCentavos = floatToCentavos(charge.FeeDetails.Amount)
	}

	platformFee, netAmount := domain.NetAmountForCharge(grossCentavos, openpayFeeCentavos, tenant.PlatformFeeBPS)

	fees := repository.PaymentFees{
		GrossAmount: grossCentavos,
		OpenpayFee:  openpayFeeCentavos,
		PlatformFee: platformFee,
		NetAmount:   netAmount,
	}

	log.Info().
		Int64("gross_centavos", grossCentavos).
		Int64("openpay_fee", openpayFeeCentavos).
		Int64("platform_fee", platformFee).
		Int64("net_amount", netAmount).
		Msg("settling charge")

	// Settle the payment atomically (fee fields + status = completed).
	if err := h.payments.SettlePayment(ctx, payment.ID, fees); err != nil {
		return fmt.Errorf("settle payment %s: %w", payment.ID, err)
	}

	// Move funds: pending balance decreases by gross, available increases by net.
	// The difference (openpay_fee + platform_fee) is silently dropped — it was
	// never money the tenant was going to receive.
	if err := h.balances.CreditSettlement(ctx, payment.TenantID, grossCentavos, netAmount, payment.Currency); err != nil {
		// Non-fatal: log loudly, but don't return 500 — the payment is already
		// settled and returning 500 would cause OpenPay to re-deliver, leading to
		// a double-settle.  A reconciliation job should detect the balance mismatch.
		log.Error().
			Err(err).
			Str("payment_id", payment.ID.String()).
			Msg("CRITICAL: payment settled but balance credit failed — manual reconciliation required")
	}

	// Publish event to Kafka → outbound webhook dispatcher + streaming RPC.
	evt := domain.PaymentEvent{
		EventID:    event.EventDate + ":" + charge.ID,
		PaymentID:  payment.ID.String(),
		TenantID:   payment.TenantID.String(),
		MemberID:   payment.MemberID.String(),
		Status:     domain.PaymentStatusCompleted,
		EventType:  event.Type,
		OccurredAt: time.Now(),
	}
	if err := h.publisher.PublishPaymentEvent(ctx, evt); err != nil {
		// Non-fatal: the payment is settled; missing a Kafka message is recoverable.
		log.Warn().Err(err).Msg("failed to publish payment event")
	}

	return nil
}

// ─── charge.failed / cancelled / refunded / chargeback ───────────────────────

func (h *IngressHandler) handleChargeStatus(
	ctx context.Context,
	event domain.OpenPayEvent,
	status domain.PaymentStatus,
	log zerolog.Logger,
) error {
	charge, err := parseCharge(event)
	if err != nil {
		return fmt.Errorf("parse charge: %w", err)
	}

	log = log.With().Str("openpay_tx_id", charge.ID).Logger()

	payment, err := h.payments.GetByOpenpayTransactionID(ctx, charge.ID)
	if err != nil {
		return fmt.Errorf("get payment by openpay tx %s: %w", charge.ID, err)
	}

	errMsg := charge.ErrorMessage
	if err := h.payments.UpdateStatus(ctx, payment.ID, status, errMsg); err != nil {
		return fmt.Errorf("update payment status to %s: %w", status, err)
	}

	// If the payment was pending/in_progress, its gross_amount was added to the
	// tenant's pending balance when the charge was created.  On failure/cancel
	// we must release that hold.
	if payment.GrossAmount > 0 {
		// Deduct from pending (noop if it was already 0 due to a race).
		// A negative grossAmount passed to DebitAvailable is nonsensical so we
		// call CreditSettlement with 0 net to collapse pending → available at $0.
		if dbErr := h.balances.CreditSettlement(ctx, payment.TenantID, payment.GrossAmount, 0, payment.Currency); dbErr != nil {
			log.Error().Err(dbErr).Msg("failed to release pending balance on failed charge")
		}
	}

	// Publish status-change event.
	evt := domain.PaymentEvent{
		EventID:    event.EventDate + ":" + charge.ID,
		PaymentID:  payment.ID.String(),
		TenantID:   payment.TenantID.String(),
		MemberID:   payment.MemberID.String(),
		Status:     status,
		EventType:  event.Type,
		OccurredAt: time.Now(),
	}
	if err := h.publisher.PublishPaymentEvent(ctx, evt); err != nil {
		log.Warn().Err(err).Msg("failed to publish payment event")
	}

	log.Info().Str("status", string(status)).Msg("payment status updated")
	return nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// parseCharge unmarshals the transaction field from an OpenPay event envelope.
func parseCharge(event domain.OpenPayEvent) (*openpay.Charge, error) {
	if event.Transaction == nil {
		return nil, fmt.Errorf("event %q has no transaction payload", event.Type)
	}
	var charge openpay.Charge
	if err := json.Unmarshal(event.Transaction, &charge); err != nil {
		return nil, fmt.Errorf("unmarshal charge: %w", err)
	}
	if charge.ID == "" {
		return nil, fmt.Errorf("charge has empty ID")
	}
	return &charge, nil
}

// floatToCentavos converts an OpenPay float64 peso amount to int64 centavos.
// e.g. 500.00 → 50000, 17.50 → 1750.
// Uses Round (not Trunc) to handle floating-point representation noise.
func floatToCentavos(pesos float64) int64 {
	return int64(math.Round(pesos * 100))
}
