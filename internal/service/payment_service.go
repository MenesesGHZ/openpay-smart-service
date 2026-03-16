// Package service contains the gRPC handler implementations that sit between
// the generated protobuf layer and the repository/domain layer.
//
// IMPORTANT: This file imports generated code from gen/openpay/v1.
// Run `make proto` before building — the gen/ directory is not committed.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	openpayv1 "github.com/menesesghz/openpay-smart-service/gen/openpay/v1"
	"github.com/menesesghz/openpay-smart-service/internal/domain"
	"github.com/menesesghz/openpay-smart-service/internal/middleware"
	"github.com/menesesghz/openpay-smart-service/internal/openpay"
	"github.com/menesesghz/openpay-smart-service/internal/repository"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// PaymentService implements openpayv1.PaymentServiceServer.
type PaymentService struct {
	openpayv1.UnimplementedPaymentServiceServer

	payments repository.PaymentRepository
	opClient *openpay.Client
	log      zerolog.Logger
}

// NewPaymentService constructs a PaymentService.
func NewPaymentService(
	payments repository.PaymentRepository,
	opClient *openpay.Client,
	log zerolog.Logger,
) *PaymentService {
	return &PaymentService{
		payments: payments,
		opClient: opClient,
		log:      log.With().Str("service", "payment").Logger(),
	}
}

// ── GetPaymentStatus ──────────────────────────────────────────────────────────

// GetPaymentStatus retrieves a single payment by its internal UUID or by its
// OpenPay transaction ID (prefix the ID with "op:" to look up by OpenPay ID).
func (s *PaymentService) GetPaymentStatus(ctx context.Context, req *openpayv1.GetPaymentStatusRequest) (*openpayv1.GetPaymentStatusResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	var (
		payment *domain.Payment
		err     error
	)

	if strings.HasPrefix(req.PaymentId, "op:") {
		// Caller passed an OpenPay transaction ID.
		txID := strings.TrimPrefix(req.PaymentId, "op:")
		payment, err = s.payments.GetByOpenpayTransactionID(ctx, txID)
		// Enforce tenant isolation: a tenant must not see another tenant's payment.
		if err == nil && payment.TenantID != tc.Tenant.ID {
			return nil, status.Error(codes.NotFound, "payment not found")
		}
	} else {
		id, parseErr := uuid.Parse(req.PaymentId)
		if parseErr != nil {
			return nil, status.Error(codes.InvalidArgument, "payment_id must be a UUID or 'op:<openpay_tx_id>'")
		}
		payment, err = s.payments.GetByID(ctx, tc.Tenant.ID, id)
	}

	if err != nil {
		return nil, domainErrToStatus(err)
	}

	return &openpayv1.GetPaymentStatusResponse{
		Payment: domainPaymentToProto(payment),
	}, nil
}

// ── ListTenantPayments ────────────────────────────────────────────────────────

// ListTenantPayments returns a filtered, cursor-paginated list of all payments
// for the authenticated tenant. The member_id filter field is ignored here —
// use ListPayments (member-scoped) for per-member views.
func (s *PaymentService) ListTenantPayments(ctx context.Context, req *openpayv1.ListTenantPaymentsRequest) (*openpayv1.ListTenantPaymentsResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	opts := repository.ListPaymentsOptions{
		TenantID:  tc.Tenant.ID,
		PageSize:  int(req.PageSize),
		PageToken: req.PageToken,
	}
	applyFilter(&opts, req.Filter)

	payments, nextToken, err := s.payments.List(ctx, opts)
	if err != nil {
		return nil, domainErrToStatus(err)
	}

	proto := make([]*openpayv1.Payment, len(payments))
	for i, p := range payments {
		proto[i] = domainPaymentToProto(p)
	}

	return &openpayv1.ListTenantPaymentsResponse{
		Payments: proto,
		PageInfo: &openpayv1.PageInfo{
			NextPageToken: nextToken,
		},
	}, nil
}

// ── ListPayments ──────────────────────────────────────────────────────────────

// ListPayments returns payments scoped to a specific member within the tenant.
// The member_id filter is required; callers should use ListTenantPayments for
// a tenant-wide view.
func (s *PaymentService) ListPayments(ctx context.Context, req *openpayv1.ListPaymentsRequest) (*openpayv1.ListPaymentsResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	opts := repository.ListPaymentsOptions{
		TenantID:  tc.Tenant.ID,
		PageSize:  int(req.PageSize),
		PageToken: req.PageToken,
	}
	applyFilter(&opts, req.Filter)

	// For member-scoped listing, member_id in the filter IS respected.
	if req.Filter != nil && req.Filter.MemberId != "" {
		memberID, err := uuid.Parse(req.Filter.MemberId)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "filter.member_id must be a valid UUID")
		}
		opts.MemberID = &memberID
	}

	payments, nextToken, err := s.payments.List(ctx, opts)
	if err != nil {
		return nil, domainErrToStatus(err)
	}

	proto := make([]*openpayv1.Payment, len(payments))
	for i, p := range payments {
		proto[i] = domainPaymentToProto(p)
	}

	return &openpayv1.ListPaymentsResponse{
		Payments: proto,
		PageInfo: &openpayv1.PageInfo{
			NextPageToken: nextToken,
		},
	}, nil
}

// ── RefundPayment ─────────────────────────────────────────────────────────────

// RefundPayment issues a refund through OpenPay and updates the local payment
// record to status=refunded.
func (s *PaymentService) RefundPayment(ctx context.Context, req *openpayv1.RefundPaymentRequest) (*openpayv1.RefundPaymentResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	paymentID, err := uuid.Parse(req.PaymentId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "payment_id must be a valid UUID")
	}

	payment, err := s.payments.GetByID(ctx, tc.Tenant.ID, paymentID)
	if err != nil {
		return nil, domainErrToStatus(err)
	}

	if payment.Status != domain.PaymentStatusCompleted {
		return nil, status.Errorf(codes.FailedPrecondition,
			"only completed payments can be refunded; current status: %s", payment.Status)
	}

	// Build refund request. A zero amount means full refund.
	refundReq := openpay.RefundRequest{Description: req.Description}
	if req.Amount > 0 {
		amountPesos := float64(req.Amount) / 100.0
		refundReq.Amount = &amountPesos
	}

	_, err = s.opClient.RefundCharge(ctx, payment.OpenpayTransactionID, refundReq)
	if err != nil {
		s.log.Error().Err(err).Str("payment_id", paymentID.String()).Msg("openpay refund failed")
		return nil, status.Errorf(codes.Internal, "refund failed: %v", err)
	}

	if err := s.payments.UpdateStatus(ctx, payment.ID, domain.PaymentStatusRefunded); err != nil {
		s.log.Error().Err(err).Str("payment_id", paymentID.String()).
			Msg("refund succeeded on OpenPay but local status update failed — needs reconciliation")
		return nil, status.Error(codes.Internal, "refund issued but status update failed")
	}

	payment.Status = domain.PaymentStatusRefunded
	return &openpayv1.RefundPaymentResponse{
		RefundPayment: domainPaymentToProto(payment),
	}, nil
}

// ── StreamPaymentEvents ───────────────────────────────────────────────────────

// StreamPaymentEvents opens a server-side stream and pushes PaymentEvents as
// they occur for the authenticated tenant.
//
// TODO: This currently returns Unimplemented. Full implementation requires a
// Kafka consumer that fans out events per-tenant to active streams.
// Tracked in: https://github.com/menesesghz/openpay-smart-service/issues/TODO
func (s *PaymentService) StreamPaymentEvents(_ *openpayv1.StreamPaymentEventsRequest, _ openpayv1.PaymentService_StreamPaymentEventsServer) error {
	return status.Error(codes.Unimplemented, "StreamPaymentEvents not yet implemented — use webhooks for real-time notifications")
}

// ── domain → proto converters ─────────────────────────────────────────────────

func domainPaymentToProto(p *domain.Payment) *openpayv1.Payment {
	proto := &openpayv1.Payment{
		Id:                   p.ID.String(),
		TenantId:             p.TenantID.String(),
		MemberId:             p.MemberID.String(),
		OpenpayTransactionId: p.OpenpayTransactionID,
		OrderId:              p.OrderID,
		IdempotencyKey:       p.IdempotencyKey,
		GrossAmount:          toMoney(p.GrossAmount, p.Currency),
		OpenpayFee:           toMoney(p.OpenpayFee, p.Currency),
		PlatformFee:          toMoney(p.PlatformFee, p.Currency),
		NetAmount:            toMoney(p.NetAmount, p.Currency),
		Method:               domainMethodToProto(p.Method),
		Status:               domainStatusToProto(p.Status),
		Description:          p.Description,
		ErrorMessage:         p.ErrorMessage,
		ErrorCode:            p.ErrorCode,
		CreatedAt:            timestamppb.New(p.CreatedAt),
		UpdatedAt:            timestamppb.New(p.UpdatedAt),
	}

	if p.LinkID != nil {
		proto.LinkId = p.LinkID.String()
	}

	if p.BankCLABE != "" {
		proto.BankDetails = &openpayv1.BankPaymentDetails{
			Clabe:     p.BankCLABE,
			BankName:  p.BankName,
			Reference: p.BankReference,
			Agreement: p.BankAgreement,
		}
	}

	if len(p.Metadata) > 0 {
		var m map[string]any
		if err := json.Unmarshal(p.Metadata, &m); err == nil {
			if s, err := structpb.NewStruct(m); err == nil {
				proto.Metadata = s
			}
		}
	}

	return proto
}

func toMoney(centavos int64, currency string) *openpayv1.Money {
	cur := openpayv1.Currency_CURRENCY_MXN
	if currency == "USD" {
		cur = openpayv1.Currency_CURRENCY_USD
	}
	return &openpayv1.Money{Amount: centavos, Currency: cur}
}

func domainStatusToProto(s domain.PaymentStatus) openpayv1.PaymentStatus {
	switch s {
	case domain.PaymentStatusPending:
		return openpayv1.PaymentStatus_PAYMENT_STATUS_PENDING
	case domain.PaymentStatusInProgress:
		return openpayv1.PaymentStatus_PAYMENT_STATUS_IN_PROGRESS
	case domain.PaymentStatusCompleted:
		return openpayv1.PaymentStatus_PAYMENT_STATUS_COMPLETED
	case domain.PaymentStatusFailed:
		return openpayv1.PaymentStatus_PAYMENT_STATUS_FAILED
	case domain.PaymentStatusCancelled:
		return openpayv1.PaymentStatus_PAYMENT_STATUS_CANCELLED
	case domain.PaymentStatusRefunded:
		return openpayv1.PaymentStatus_PAYMENT_STATUS_REFUNDED
	case domain.PaymentStatusChargeback:
		return openpayv1.PaymentStatus_PAYMENT_STATUS_CHARGEBACK
	default:
		return openpayv1.PaymentStatus_PAYMENT_STATUS_UNSPECIFIED
	}
}

func domainMethodToProto(m domain.PaymentMethod) openpayv1.PaymentMethod {
	switch m {
	case domain.PaymentMethodCard:
		return openpayv1.PaymentMethod_PAYMENT_METHOD_CARD
	case domain.PaymentMethodBankAccount:
		return openpayv1.PaymentMethod_PAYMENT_METHOD_BANK_ACCOUNT
	case domain.PaymentMethodStore:
		return openpayv1.PaymentMethod_PAYMENT_METHOD_STORE
	default:
		return openpayv1.PaymentMethod_PAYMENT_METHOD_UNSPECIFIED
	}
}

// ── filter mapping ────────────────────────────────────────────────────────────

// applyFilter maps a proto PaymentFilter onto a ListPaymentsOptions.
// member_id is intentionally NOT mapped here — callers handle it themselves
// since ListTenantPayments ignores it while ListPayments requires it.
func applyFilter(opts *repository.ListPaymentsOptions, f *openpayv1.PaymentFilter) {
	if f == nil {
		return
	}

	for _, s := range f.Status {
		opts.Statuses = append(opts.Statuses, protoStatusToDomain(s))
	}
	for _, m := range f.Method {
		opts.Methods = append(opts.Methods, protoMethodToDomain(m))
	}
	if f.Currency == openpayv1.Currency_CURRENCY_MXN {
		opts.Currency = "MXN"
	} else if f.Currency == openpayv1.Currency_CURRENCY_USD {
		opts.Currency = "USD"
	}
	if f.AmountMin > 0 {
		opts.AmountMin = &f.AmountMin
	}
	if f.AmountMax > 0 {
		opts.AmountMax = &f.AmountMax
	}
	if f.OrderId != "" {
		opts.OrderID = f.OrderId
	}
	if f.From != nil {
		t := f.From.AsTime()
		opts.From = &t
	}
	if f.To != nil {
		t := f.To.AsTime()
		opts.To = &t
	}
}

func protoStatusToDomain(s openpayv1.PaymentStatus) string {
	switch s {
	case openpayv1.PaymentStatus_PAYMENT_STATUS_PENDING:
		return string(domain.PaymentStatusPending)
	case openpayv1.PaymentStatus_PAYMENT_STATUS_IN_PROGRESS:
		return string(domain.PaymentStatusInProgress)
	case openpayv1.PaymentStatus_PAYMENT_STATUS_COMPLETED:
		return string(domain.PaymentStatusCompleted)
	case openpayv1.PaymentStatus_PAYMENT_STATUS_FAILED:
		return string(domain.PaymentStatusFailed)
	case openpayv1.PaymentStatus_PAYMENT_STATUS_CANCELLED:
		return string(domain.PaymentStatusCancelled)
	case openpayv1.PaymentStatus_PAYMENT_STATUS_REFUNDED:
		return string(domain.PaymentStatusRefunded)
	case openpayv1.PaymentStatus_PAYMENT_STATUS_CHARGEBACK:
		return string(domain.PaymentStatusChargeback)
	default:
		return ""
	}
}

func protoMethodToDomain(m openpayv1.PaymentMethod) string {
	switch m {
	case openpayv1.PaymentMethod_PAYMENT_METHOD_CARD:
		return string(domain.PaymentMethodCard)
	case openpayv1.PaymentMethod_PAYMENT_METHOD_BANK_ACCOUNT:
		return string(domain.PaymentMethodBankAccount)
	case openpayv1.PaymentMethod_PAYMENT_METHOD_STORE:
		return string(domain.PaymentMethodStore)
	default:
		return ""
	}
}

// ── error mapping ─────────────────────────────────────────────────────────────

// domainErrToStatus converts domain sentinel errors to gRPC status errors.
func domainErrToStatus(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, domain.ErrAlreadyExists):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, domain.ErrInvalidArgument):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, domain.ErrPermissionDenied):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, domain.ErrUnauthenticated):
		return status.Error(codes.Unauthenticated, err.Error())
	case errors.Is(err, domain.ErrRateLimitExceeded):
		return status.Error(codes.ResourceExhausted, err.Error())
	default:
		return status.Error(codes.Internal, "internal error")
	}
}

// ensure time import is used (used by applyFilter via f.From.AsTime())
var _ = time.Time{}
