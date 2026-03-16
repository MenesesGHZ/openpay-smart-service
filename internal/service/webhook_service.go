package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	openpayv1 "github.com/menesesghz/openpay-smart-service/gen/openpay/v1"
	"github.com/menesesghz/openpay-smart-service/internal/domain"
	"github.com/menesesghz/openpay-smart-service/internal/encrypt"
	"github.com/menesesghz/openpay-smart-service/internal/middleware"
	"github.com/menesesghz/openpay-smart-service/internal/repository"
)

// WebhookService implements openpayv1.WebhookServiceServer.
type WebhookService struct {
	openpayv1.UnimplementedWebhookServiceServer

	webhookRepo repository.WebhookRepository
	aesKey      string
	log         zerolog.Logger
}

func NewWebhookService(webhookRepo repository.WebhookRepository, aesKey string, log zerolog.Logger) *WebhookService {
	return &WebhookService{
		webhookRepo: webhookRepo,
		aesKey:      aesKey,
		log:         log.With().Str("service", "webhook").Logger(),
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func generateWebhookSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "whsec_" + hex.EncodeToString(b), nil
}

func webhookToProto(w *domain.WebhookSubscription) *openpayv1.WebhookSubscription {
	p := &openpayv1.WebhookSubscription{
		Id:        w.ID.String(),
		TenantId:  w.TenantID.String(),
		Url:       w.URL,
		Events:    w.Events,
		Enabled:   w.Enabled,
		CreatedAt: timestamppb.New(w.CreatedAt),
		UpdatedAt: timestamppb.New(w.UpdatedAt),
	}
	if w.RetryPolicy.MaxAttempts > 0 {
		intervals := make([]int32, len(w.RetryPolicy.IntervalsSec))
		for i, v := range w.RetryPolicy.IntervalsSec {
			intervals[i] = int32(v)
		}
		p.RetryPolicy = &openpayv1.RetryPolicy{
			MaxAttempts:  int32(w.RetryPolicy.MaxAttempts),
			IntervalsSec: intervals,
		}
	}
	return p
}

func deliveryToProto(d *domain.WebhookDelivery) *openpayv1.WebhookDelivery {
	p := &openpayv1.WebhookDelivery{
		Id:             d.ID.String(),
		SubscriptionId: d.SubscriptionID.String(),
		EventType:      d.EventType,
		Attempts:       int32(d.Attempts),
		ResponseCode:   int32(d.ResponseCode),
		LatencyMs:      d.LatencyMS,
		ErrorMessage:   d.ErrorMessage,
		CreatedAt:      timestamppb.New(d.CreatedAt),
	}
	switch d.Status {
	case domain.DeliveryStatusDelivered:
		p.Status = openpayv1.DeliveryStatus_DELIVERY_STATUS_DELIVERED
	case domain.DeliveryStatusFailed:
		p.Status = openpayv1.DeliveryStatus_DELIVERY_STATUS_FAILED
	case domain.DeliveryStatusDLQ:
		p.Status = openpayv1.DeliveryStatus_DELIVERY_STATUS_DLQ
	default:
		p.Status = openpayv1.DeliveryStatus_DELIVERY_STATUS_PENDING
	}
	if d.LastAttemptedAt != nil {
		p.LastAttemptedAt = timestamppb.New(*d.LastAttemptedAt)
	}
	if d.NextRetryAt != nil {
		p.NextRetryAt = timestamppb.New(*d.NextRetryAt)
	}
	// Best-effort parse of JSON payload into proto Struct.
	if len(d.Payload) > 0 {
		s := &structpb.Struct{}
		if err := s.UnmarshalJSON(d.Payload); err == nil {
			p.Payload = s
		}
	}
	return p
}

// ── RegisterWebhook ───────────────────────────────────────────────────────────

func (s *WebhookService) RegisterWebhook(ctx context.Context, req *openpayv1.RegisterWebhookRequest) (*openpayv1.RegisterWebhookResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	if req.Url == "" {
		return nil, status.Error(codes.InvalidArgument, "url is required")
	}
	if len(req.Events) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one event type is required")
	}

	rawSecret, err := generateWebhookSecret()
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to generate webhook secret")
	}
	encSecret, err := encrypt.Encrypt(s.aesKey, rawSecret)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to encrypt webhook secret")
	}

	policy := domain.DefaultRetryPolicy
	if req.RetryPolicy != nil && req.RetryPolicy.MaxAttempts > 0 {
		intervals := make([]int, len(req.RetryPolicy.IntervalsSec))
		for i, v := range req.RetryPolicy.IntervalsSec {
			intervals[i] = int(v)
		}
		policy = domain.RetryPolicy{
			MaxAttempts:  int(req.RetryPolicy.MaxAttempts),
			IntervalsSec: intervals,
		}
	}

	w := &domain.WebhookSubscription{
		TenantID:    tc.Tenant.ID,
		URL:         req.Url,
		SecretEnc:   encSecret,
		Events:      req.Events,
		RetryPolicy: policy,
		Enabled:     true,
	}

	if err := s.webhookRepo.CreateSubscription(ctx, w); err != nil {
		s.log.Error().Err(err).Str("tenant_id", tc.Tenant.ID.String()).Msg("register webhook failed")
		return nil, status.Error(codes.Internal, "failed to register webhook")
	}

	s.log.Info().Str("webhook_id", w.ID.String()).Str("url", w.URL).Msg("webhook registered")
	return &openpayv1.RegisterWebhookResponse{
		Webhook: webhookToProto(w),
		Secret:  rawSecret,
	}, nil
}

// ── UpdateWebhook ─────────────────────────────────────────────────────────────

func (s *WebhookService) UpdateWebhook(ctx context.Context, req *openpayv1.UpdateWebhookRequest) (*openpayv1.UpdateWebhookResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	id, err := uuid.Parse(req.WebhookId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid webhook_id")
	}

	w, err := s.webhookRepo.GetSubscription(ctx, tc.Tenant.ID, id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "webhook not found")
		}
		return nil, status.Error(codes.Internal, "failed to fetch webhook")
	}

	if req.Url != "" {
		w.URL = req.Url
	}
	if len(req.Events) > 0 {
		w.Events = req.Events
	}
	w.Enabled = req.Enabled
	if req.RetryPolicy != nil && req.RetryPolicy.MaxAttempts > 0 {
		intervals := make([]int, len(req.RetryPolicy.IntervalsSec))
		for i, v := range req.RetryPolicy.IntervalsSec {
			intervals[i] = int(v)
		}
		w.RetryPolicy = domain.RetryPolicy{
			MaxAttempts:  int(req.RetryPolicy.MaxAttempts),
			IntervalsSec: intervals,
		}
	}

	if err := s.webhookRepo.UpdateSubscription(ctx, w); err != nil {
		return nil, status.Error(codes.Internal, "failed to update webhook")
	}
	return &openpayv1.UpdateWebhookResponse{Webhook: webhookToProto(w)}, nil
}

// ── DeleteWebhook ─────────────────────────────────────────────────────────────

func (s *WebhookService) DeleteWebhook(ctx context.Context, req *openpayv1.DeleteWebhookRequest) (*openpayv1.DeleteWebhookResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	id, err := uuid.Parse(req.WebhookId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid webhook_id")
	}

	if err := s.webhookRepo.DeleteSubscription(ctx, tc.Tenant.ID, id); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "webhook not found")
		}
		return nil, status.Error(codes.Internal, "failed to delete webhook")
	}
	return &openpayv1.DeleteWebhookResponse{Success: true}, nil
}

// ── ListWebhooks ──────────────────────────────────────────────────────────────

func (s *WebhookService) ListWebhooks(ctx context.Context, req *openpayv1.ListWebhooksRequest) (*openpayv1.ListWebhooksResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	pageSize := int(req.PageSize)
	subs, nextToken, err := s.webhookRepo.ListSubscriptions(ctx, tc.Tenant.ID, req.Enabled, pageSize, req.PageToken)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list webhooks")
	}

	out := make([]*openpayv1.WebhookSubscription, len(subs))
	for i, w := range subs {
		out[i] = webhookToProto(w)
	}
	return &openpayv1.ListWebhooksResponse{
		Webhooks: out,
		PageInfo: &openpayv1.PageInfo{NextPageToken: nextToken},
	}, nil
}

// ── GetWebhookDelivery ────────────────────────────────────────────────────────

func (s *WebhookService) GetWebhookDelivery(ctx context.Context, req *openpayv1.GetWebhookDeliveryRequest) (*openpayv1.GetWebhookDeliveryResponse, error) {
	_, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	id, err := uuid.Parse(req.DeliveryId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid delivery_id")
	}

	d, err := s.webhookRepo.GetDelivery(ctx, id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "delivery not found")
		}
		return nil, status.Error(codes.Internal, "failed to fetch delivery")
	}
	return &openpayv1.GetWebhookDeliveryResponse{Delivery: deliveryToProto(d)}, nil
}

// ── RetryWebhookDelivery ──────────────────────────────────────────────────────

func (s *WebhookService) RetryWebhookDelivery(ctx context.Context, req *openpayv1.RetryWebhookDeliveryRequest) (*openpayv1.RetryWebhookDeliveryResponse, error) {
	_, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	id, err := uuid.Parse(req.DeliveryId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid delivery_id")
	}

	d, err := s.webhookRepo.GetDelivery(ctx, id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "delivery not found")
		}
		return nil, status.Error(codes.Internal, "failed to fetch delivery")
	}

	// Reset to pending so the worker picks it up again.
	d.Status = domain.DeliveryStatusPending
	d.NextRetryAt = nil
	if err := s.webhookRepo.UpdateDelivery(ctx, d); err != nil {
		return nil, status.Error(codes.Internal, "failed to queue retry")
	}

	s.log.Info().Str("delivery_id", id.String()).Msg("webhook delivery queued for retry")
	return &openpayv1.RetryWebhookDeliveryResponse{Delivery: deliveryToProto(d)}, nil
}

// ── RotateWebhookSecret ───────────────────────────────────────────────────────

func (s *WebhookService) RotateWebhookSecret(ctx context.Context, req *openpayv1.RotateWebhookSecretRequest) (*openpayv1.RotateWebhookSecretResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	id, err := uuid.Parse(req.WebhookId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid webhook_id")
	}

	w, err := s.webhookRepo.GetSubscription(ctx, tc.Tenant.ID, id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "webhook not found")
		}
		return nil, status.Error(codes.Internal, "failed to fetch webhook")
	}

	newSecret, err := generateWebhookSecret()
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to generate secret")
	}
	encSecret, err := encrypt.Encrypt(s.aesKey, newSecret)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to encrypt secret")
	}

	w.SecretEnc = encSecret
	if err := s.webhookRepo.UpdateSubscription(ctx, w); err != nil {
		return nil, status.Error(codes.Internal, "failed to rotate secret")
	}

	s.log.Info().Str("webhook_id", id.String()).Msg("webhook secret rotated")
	return &openpayv1.RotateWebhookSecretResponse{NewSecret: newSecret}, nil
}
