package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog"
	"github.com/your-org/openpay-smart-service/internal/domain"
	"github.com/your-org/openpay-smart-service/internal/repository"
)

// Dispatcher sends webhook payloads to tenant endpoints and manages retry logic.
type Dispatcher struct {
	repo        repository.WebhookRepository
	http        *http.Client
	log         zerolog.Logger
	timeoutMS   int
	maxAttempts int
	intervals   []int // retry delay per attempt in seconds
}

func NewDispatcher(
	repo repository.WebhookRepository,
	log zerolog.Logger,
	timeoutMS, maxAttempts int,
	intervals []int,
) *Dispatcher {
	return &Dispatcher{
		repo: repo,
		http: &http.Client{Timeout: time.Duration(timeoutMS) * time.Millisecond},
		log:  log.With().Str("component", "webhook_dispatcher").Logger(),
		timeoutMS:   timeoutMS,
		maxAttempts: maxAttempts,
		intervals:   intervals,
	}
}

// Dispatch attempts to deliver a single WebhookDelivery. It updates the delivery
// record with the outcome and schedules a retry if warranted.
func (d *Dispatcher) Dispatch(ctx context.Context, delivery *domain.WebhookDelivery, sub *domain.WebhookSubscription, secret string) error {
	payload, err := json.Marshal(delivery.Payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	sig := SignPayload(secret, payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.URL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-OpenPay-Smart-Signature", sig)
	req.Header.Set("X-OpenPay-Smart-Event", delivery.EventType)
	req.Header.Set("X-OpenPay-Smart-Delivery-ID", delivery.ID.String())

	start := time.Now()
	resp, err := d.http.Do(req)
	latencyMS := time.Since(start).Milliseconds()

	delivery.Attempts++
	now := time.Now()
	delivery.LastAttemptedAt = &now
	delivery.LatencyMS = latencyMS

	if err != nil {
		delivery.ErrorMessage = err.Error()
		delivery.ResponseCode = 0
		return d.handleFailure(ctx, delivery)
	}
	defer resp.Body.Close()

	delivery.ResponseCode = resp.StatusCode

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		delivery.Status = domain.DeliveryStatusDelivered
		delivery.ErrorMessage = ""
		d.log.Info().
			Str("delivery_id", delivery.ID.String()).
			Str("url", sub.URL).
			Int("status_code", resp.StatusCode).
			Int64("latency_ms", latencyMS).
			Msg("webhook delivered")
	} else {
		delivery.ErrorMessage = fmt.Sprintf("non-2xx response: %d", resp.StatusCode)
		return d.handleFailure(ctx, delivery)
	}

	return d.repo.UpdateDelivery(ctx, delivery)
}

func (d *Dispatcher) handleFailure(ctx context.Context, delivery *domain.WebhookDelivery) error {
	if delivery.Attempts >= d.maxAttempts {
		delivery.Status = domain.DeliveryStatusDLQ
		d.log.Warn().
			Str("delivery_id", delivery.ID.String()).
			Int("attempts", delivery.Attempts).
			Msg("webhook moved to DLQ after exhausting all retries")
	} else {
		delivery.Status = domain.DeliveryStatusFailed
		idx := delivery.Attempts - 1
		if idx >= len(d.intervals) {
			idx = len(d.intervals) - 1
		}
		retryAt := time.Now().Add(time.Duration(d.intervals[idx]) * time.Second)
		delivery.NextRetryAt = &retryAt
	}
	return d.repo.UpdateDelivery(ctx, delivery)
}
