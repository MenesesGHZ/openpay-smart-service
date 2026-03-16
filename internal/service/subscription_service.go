package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	openpayv1 "github.com/menesesghz/openpay-smart-service/gen/openpay/v1"
	"github.com/menesesghz/openpay-smart-service/internal/domain"
	"github.com/menesesghz/openpay-smart-service/internal/middleware"
	"github.com/menesesghz/openpay-smart-service/internal/openpay"
	"github.com/menesesghz/openpay-smart-service/internal/repository"
)

// SubscriptionService implements openpayv1.SubscriptionServiceServer.
//
// It manages Plans and Subscriptions for multi-tenant recurring billing.
// Each tenant creates their own Plans; members are subscribed to plans via
// stored cards, and OpenPay handles the automatic periodic charging.
type SubscriptionService struct {
	openpayv1.UnimplementedSubscriptionServiceServer

	plans         repository.PlanRepository
	subscriptions repository.SubscriptionRepository
	payments      repository.PaymentRepository
	members       repository.MemberRepository
	opClient      *openpay.Client
	log           zerolog.Logger
}

// NewSubscriptionService constructs a SubscriptionService.
func NewSubscriptionService(
	plans repository.PlanRepository,
	subscriptions repository.SubscriptionRepository,
	payments repository.PaymentRepository,
	members repository.MemberRepository,
	opClient *openpay.Client,
	log zerolog.Logger,
) *SubscriptionService {
	return &SubscriptionService{
		plans:         plans,
		subscriptions: subscriptions,
		payments:      payments,
		members:       members,
		opClient:      opClient,
		log:           log.With().Str("service", "subscription").Logger(),
	}
}

// ─── Plan RPCs ────────────────────────────────────────────────────────────────

// CreatePlan creates a billing plan on OpenPay and persists it locally.
func (s *SubscriptionService) CreatePlan(ctx context.Context, req *openpayv1.CreatePlanRequest) (*openpayv1.CreatePlanResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}
	if req.Amount == nil || req.Amount.Amount <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount must be positive")
	}
	if req.RepeatUnit == openpayv1.RepeatUnit_REPEAT_UNIT_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "repeat_unit is required")
	}

	repeatEvery := int(req.RepeatEvery)
	if repeatEvery <= 0 {
		repeatEvery = 1
	}
	retryTimes := int(req.RetryTimes)
	if retryTimes <= 0 {
		retryTimes = 3
	}
	statusOnRetryEnd := req.StatusOnRetryEnd
	if statusOnRetryEnd == "" {
		statusOnRetryEnd = "cancelled"
	}
	if statusOnRetryEnd != "cancelled" && statusOnRetryEnd != "unpaid" {
		return nil, status.Error(codes.InvalidArgument, "status_on_retry_end must be 'cancelled' or 'unpaid'")
	}

	currency := "MXN"
	if req.Amount.Currency == openpayv1.Currency_CURRENCY_USD {
		currency = "USD"
	}

	amountPesos := float64(req.Amount.Amount) / 100.0
	repeatUnit := protoRepeatUnitToDomain(req.RepeatUnit)

	opPlan, err := s.opClient.CreatePlan(ctx, openpay.CreatePlanRequest{
		Name:             req.Name,
		Amount:           amountPesos,
		Currency:         currency,
		RepeatEvery:      repeatEvery,
		RepeatUnit:       repeatUnit,
		RetryTimes:       retryTimes,
		StatusOnRetryEnd: statusOnRetryEnd,
		TrialDays:        int(req.TrialDays),
	})
	if err != nil {
		s.log.Error().Err(err).Msg("openpay create plan failed")
		return nil, status.Errorf(codes.Internal, "create plan on OpenPay: %v", err)
	}

	plan := &domain.Plan{
		TenantID:         tc.Tenant.ID,
		OpenpayPlanID:    opPlan.ID,
		Name:             req.Name,
		Amount:           req.Amount.Amount,
		Currency:         currency,
		RepeatEvery:      repeatEvery,
		RepeatUnit:       repeatUnit,
		TrialDays:        int(req.TrialDays),
		RetryTimes:       retryTimes,
		StatusOnRetryEnd: statusOnRetryEnd,
		Active:           true,
	}
	if err := s.plans.Create(ctx, plan); err != nil {
		s.log.Error().Err(err).Str("openpay_plan_id", opPlan.ID).
			Msg("plan created on OpenPay but failed to persist locally")
		return nil, status.Error(codes.Internal, "persist plan failed")
	}

	return &openpayv1.CreatePlanResponse{Plan: domainPlanToProto(plan)}, nil
}

// GetPlan retrieves a plan by its internal UUID.
func (s *SubscriptionService) GetPlan(ctx context.Context, req *openpayv1.GetPlanRequest) (*openpayv1.GetPlanResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}
	planID, err := uuid.Parse(req.PlanId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid plan_id")
	}

	plan, err := s.plans.GetByID(ctx, tc.Tenant.ID, planID)
	if err != nil {
		return nil, domainErrToStatus(err)
	}
	return &openpayv1.GetPlanResponse{Plan: domainPlanToProto(plan)}, nil
}

// ListPlans returns all plans for the authenticated tenant, with cursor pagination.
func (s *SubscriptionService) ListPlans(ctx context.Context, req *openpayv1.ListPlansRequest) (*openpayv1.ListPlansResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	plans, nextToken, err := s.plans.List(ctx, repository.ListPlansOptions{
		TenantID:   tc.Tenant.ID,
		ActiveOnly: req.ActiveOnly,
		PageSize:   int(req.PageSize),
		PageToken:  req.PageToken,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list plans: %v", err)
	}

	out := make([]*openpayv1.Plan, len(plans))
	for i, p := range plans {
		out[i] = domainPlanToProto(p)
	}
	return &openpayv1.ListPlansResponse{
		Plans:    out,
		PageInfo: &openpayv1.PageInfo{NextPageToken: nextToken},
	}, nil
}

// DeactivatePlan marks the plan inactive locally and deletes it on OpenPay.
// Existing subscriptions continue their current period but will not renew.
func (s *SubscriptionService) DeactivatePlan(ctx context.Context, req *openpayv1.DeactivatePlanRequest) (*openpayv1.DeactivatePlanResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}
	planID, err := uuid.Parse(req.PlanId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid plan_id")
	}

	plan, err := s.plans.GetByID(ctx, tc.Tenant.ID, planID)
	if err != nil {
		return nil, domainErrToStatus(err)
	}

	// Delete on OpenPay first; if it fails we don't deactivate locally.
	if err := s.opClient.DeletePlan(ctx, plan.OpenpayPlanID); err != nil {
		// If OpenPay says "not found" the plan was already deleted; proceed.
		if !errors.Is(err, domain.ErrNotFound) {
			return nil, status.Errorf(codes.Internal, "delete plan on OpenPay: %v", err)
		}
	}

	if err := s.plans.Deactivate(ctx, tc.Tenant.ID, planID); err != nil {
		return nil, status.Errorf(codes.Internal, "deactivate plan: %v", err)
	}
	return &openpayv1.DeactivatePlanResponse{}, nil
}

// ─── Subscription RPCs ────────────────────────────────────────────────────────

// CreateSubscription subscribes a member to a plan using a stored card.
// OpenPay will automatically charge the card at each billing cycle.
func (s *SubscriptionService) CreateSubscription(ctx context.Context, req *openpayv1.CreateSubscriptionRequest) (*openpayv1.CreateSubscriptionResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}
	if req.MemberId == "" {
		return nil, status.Error(codes.InvalidArgument, "member_id is required")
	}
	if req.PlanId == "" {
		return nil, status.Error(codes.InvalidArgument, "plan_id is required")
	}
	if req.SourceCardId == "" {
		return nil, status.Error(codes.InvalidArgument, "source_card_id is required")
	}

	memberID, err := uuid.Parse(req.MemberId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid member_id")
	}
	planID, err := uuid.Parse(req.PlanId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid plan_id")
	}

	// Validate member belongs to this tenant.
	member, err := s.members.GetByID(ctx, tc.Tenant.ID, memberID)
	if err != nil {
		return nil, domainErrToStatus(err)
	}
	if member.OpenpayCustomerID == "" {
		return nil, status.Error(codes.FailedPrecondition, "member has no OpenPay customer ID — register the member first")
	}

	// Validate plan belongs to this tenant.
	plan, err := s.plans.GetByID(ctx, tc.Tenant.ID, planID)
	if err != nil {
		return nil, domainErrToStatus(err)
	}
	if !plan.Active {
		return nil, status.Error(codes.FailedPrecondition, "plan is inactive")
	}

	// Create subscription on OpenPay.
	opSubReq := openpay.CreateSubscriptionRequest{
		PlanID: plan.OpenpayPlanID,
		CardID: req.SourceCardId,
	}
	if req.TrialEndDate != "" {
		opSubReq.TrialEndDate = req.TrialEndDate
	}

	opSub, err := s.opClient.CreateSubscription(ctx, member.OpenpayCustomerID, opSubReq)
	if err != nil {
		s.log.Error().Err(err).
			Str("member_id", memberID.String()).
			Str("plan_id", planID.String()).
			Msg("openpay create subscription failed")
		return nil, status.Errorf(codes.Internal, "create subscription on OpenPay: %v", err)
	}

	sub := &domain.Subscription{
		TenantID:     tc.Tenant.ID,
		MemberID:     memberID,
		PlanID:       planID,
		OpenpaySubID: opSub.ID,
		SourceCardID: req.SourceCardId,
		Status:       domain.SubscriptionStatus(opSub.Status),
	}
	if opSub.TrialEndDate != "" {
		sub.TrialEndDate = parseOpenPayDate(opSub.TrialEndDate)
	}
	if opSub.PeriodEndDate != "" {
		sub.PeriodEndDate = parseOpenPayDate(opSub.PeriodEndDate)
	}

	if err := s.subscriptions.Create(ctx, sub); err != nil {
		s.log.Error().Err(err).Str("openpay_sub_id", opSub.ID).
			Msg("subscription created on OpenPay but failed to persist locally")
		return nil, status.Error(codes.Internal, "persist subscription failed")
	}

	return &openpayv1.CreateSubscriptionResponse{Subscription: domainSubToProto(sub)}, nil
}

// GetSubscription returns a subscription by its internal UUID.
func (s *SubscriptionService) GetSubscription(ctx context.Context, req *openpayv1.GetSubscriptionRequest) (*openpayv1.GetSubscriptionResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}
	subID, err := uuid.Parse(req.SubscriptionId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid subscription_id")
	}

	sub, err := s.subscriptions.GetByID(ctx, tc.Tenant.ID, subID)
	if err != nil {
		return nil, domainErrToStatus(err)
	}
	return &openpayv1.GetSubscriptionResponse{Subscription: domainSubToProto(sub)}, nil
}

// ListSubscriptions returns subscriptions for the authenticated tenant, filtered
// by optional member_id, plan_id, and status.
func (s *SubscriptionService) ListSubscriptions(ctx context.Context, req *openpayv1.ListSubscriptionsRequest) (*openpayv1.ListSubscriptionsResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}

	opts := repository.ListSubscriptionsOptions{
		TenantID:  tc.Tenant.ID,
		PageSize:  int(req.PageSize),
		PageToken: req.PageToken,
	}
	if req.Filter != nil {
		if req.Filter.MemberId != "" {
			if id, err := uuid.Parse(req.Filter.MemberId); err == nil {
				opts.MemberID = &id
			}
		}
		if req.Filter.PlanId != "" {
			if id, err := uuid.Parse(req.Filter.PlanId); err == nil {
				opts.PlanID = &id
			}
		}
		for _, st := range req.Filter.Status {
			opts.Statuses = append(opts.Statuses, protoSubStatusToDomain(st))
		}
	}

	subs, nextToken, err := s.subscriptions.List(ctx, opts)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list subscriptions: %v", err)
	}

	out := make([]*openpayv1.Subscription, len(subs))
	for i, sub := range subs {
		out[i] = domainSubToProto(sub)
	}
	return &openpayv1.ListSubscriptionsResponse{
		Subscriptions: out,
		PageInfo:      &openpayv1.PageInfo{NextPageToken: nextToken},
	}, nil
}

// CancelSubscription cancels a subscription at the end of the current billing
// period.  The member retains access until period_end_date.
func (s *SubscriptionService) CancelSubscription(ctx context.Context, req *openpayv1.CancelSubscriptionRequest) (*openpayv1.CancelSubscriptionResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}
	subID, err := uuid.Parse(req.SubscriptionId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid subscription_id")
	}

	sub, err := s.subscriptions.GetByID(ctx, tc.Tenant.ID, subID)
	if err != nil {
		return nil, domainErrToStatus(err)
	}
	if sub.Status == domain.SubscriptionStatusCancelled || sub.Status == domain.SubscriptionStatusExpired {
		return nil, status.Error(codes.FailedPrecondition, "subscription is already cancelled or expired")
	}

	// Fetch member to get the OpenPay customer ID.
	member, err := s.members.GetByID(ctx, tc.Tenant.ID, sub.MemberID)
	if err != nil {
		return nil, domainErrToStatus(err)
	}

	// Cancel on OpenPay — this schedules cancellation at period end.
	if err := s.opClient.CancelSubscription(ctx, member.OpenpayCustomerID, sub.OpenpaySubID); err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			return nil, status.Errorf(codes.Internal, "cancel subscription on OpenPay: %v", err)
		}
	}

	// Mark locally as cancel_at_period_end = true.
	if err := s.subscriptions.SetCancelAtPeriodEnd(ctx, sub.ID); err != nil {
		return nil, status.Errorf(codes.Internal, "set cancel_at_period_end: %v", err)
	}

	sub.CancelAtPeriodEnd = true
	return &openpayv1.CancelSubscriptionResponse{Subscription: domainSubToProto(sub)}, nil
}

// ListSubscriptionPayments returns all payment records for a subscription,
// most recent first.
func (s *SubscriptionService) ListSubscriptionPayments(ctx context.Context, req *openpayv1.ListSubscriptionPaymentsRequest) (*openpayv1.ListSubscriptionPaymentsResponse, error) {
	tc, ok := middleware.TenantFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}
	subID, err := uuid.Parse(req.SubscriptionId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid subscription_id")
	}

	// Verify the subscription belongs to this tenant.
	if _, err := s.subscriptions.GetByID(ctx, tc.Tenant.ID, subID); err != nil {
		return nil, domainErrToStatus(err)
	}

	payments, nextToken, err := s.payments.List(ctx, repository.ListPaymentsOptions{
		TenantID:       tc.Tenant.ID,
		SubscriptionID: &subID,
		PageSize:       int(req.PageSize),
		PageToken:      req.PageToken,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list subscription payments: %v", err)
	}

	out := make([]*openpayv1.Payment, len(payments))
	for i, p := range payments {
		out[i] = domainPaymentToProto(p)
	}
	return &openpayv1.ListSubscriptionPaymentsResponse{
		Payments: out,
		PageInfo: &openpayv1.PageInfo{NextPageToken: nextToken},
	}, nil
}

// ─── domain → proto converters ────────────────────────────────────────────────

func domainPlanToProto(p *domain.Plan) *openpayv1.Plan {
	return &openpayv1.Plan{
		Id:                 p.ID.String(),
		TenantId:           p.TenantID.String(),
		OpenpayPlanId:      p.OpenpayPlanID,
		Name:               p.Name,
		Amount:             toMoney(p.Amount, p.Currency),
		RepeatUnit:         domainRepeatUnitToProto(p.RepeatUnit),
		RepeatEvery:        int32(p.RepeatEvery),
		TrialDays:          int32(p.TrialDays),
		RetryTimes:         int32(p.RetryTimes),
		StatusOnRetryEnd:   p.StatusOnRetryEnd,
		Active:             p.Active,
		CreatedAt:          timestamppb.New(p.CreatedAt),
		UpdatedAt:          timestamppb.New(p.UpdatedAt),
	}
}

func domainSubToProto(s *domain.Subscription) *openpayv1.Subscription {
	sub := &openpayv1.Subscription{
		Id:                 s.ID.String(),
		TenantId:           s.TenantID.String(),
		MemberId:           s.MemberID.String(),
		PlanId:             s.PlanID.String(),
		OpenpaySubId:       s.OpenpaySubID,
		SourceCardId:       s.SourceCardID,
		Status:             domainSubStatusToProto(s.Status),
		CancelAtPeriodEnd:  s.CancelAtPeriodEnd,
		FailedChargeCount:  int32(s.FailedChargeCount),
		CreatedAt:          timestamppb.New(s.CreatedAt),
		UpdatedAt:          timestamppb.New(s.UpdatedAt),
	}
	if s.TrialEndDate != nil {
		sub.TrialEndDate = timestamppb.New(*s.TrialEndDate)
	}
	if s.PeriodEndDate != nil {
		sub.PeriodEndDate = timestamppb.New(*s.PeriodEndDate)
	}
	if s.LastChargeID != nil {
		sub.LastChargeId = s.LastChargeID.String()
	}
	return sub
}

func domainSubStatusToProto(s domain.SubscriptionStatus) openpayv1.SubscriptionStatus {
	switch s {
	case domain.SubscriptionStatusTrial:
		return openpayv1.SubscriptionStatus_SUBSCRIPTION_STATUS_TRIAL
	case domain.SubscriptionStatusActive:
		return openpayv1.SubscriptionStatus_SUBSCRIPTION_STATUS_ACTIVE
	case domain.SubscriptionStatusPastDue:
		return openpayv1.SubscriptionStatus_SUBSCRIPTION_STATUS_PAST_DUE
	case domain.SubscriptionStatusUnpaid:
		return openpayv1.SubscriptionStatus_SUBSCRIPTION_STATUS_UNPAID
	case domain.SubscriptionStatusCancelled:
		return openpayv1.SubscriptionStatus_SUBSCRIPTION_STATUS_CANCELLED
	case domain.SubscriptionStatusExpired:
		return openpayv1.SubscriptionStatus_SUBSCRIPTION_STATUS_EXPIRED
	default:
		return openpayv1.SubscriptionStatus_SUBSCRIPTION_STATUS_UNSPECIFIED
	}
}

func protoSubStatusToDomain(s openpayv1.SubscriptionStatus) string {
	switch s {
	case openpayv1.SubscriptionStatus_SUBSCRIPTION_STATUS_TRIAL:
		return string(domain.SubscriptionStatusTrial)
	case openpayv1.SubscriptionStatus_SUBSCRIPTION_STATUS_ACTIVE:
		return string(domain.SubscriptionStatusActive)
	case openpayv1.SubscriptionStatus_SUBSCRIPTION_STATUS_PAST_DUE:
		return string(domain.SubscriptionStatusPastDue)
	case openpayv1.SubscriptionStatus_SUBSCRIPTION_STATUS_UNPAID:
		return string(domain.SubscriptionStatusUnpaid)
	case openpayv1.SubscriptionStatus_SUBSCRIPTION_STATUS_CANCELLED:
		return string(domain.SubscriptionStatusCancelled)
	case openpayv1.SubscriptionStatus_SUBSCRIPTION_STATUS_EXPIRED:
		return string(domain.SubscriptionStatusExpired)
	default:
		return ""
	}
}

func domainRepeatUnitToProto(unit string) openpayv1.RepeatUnit {
	switch unit {
	case "week":
		return openpayv1.RepeatUnit_REPEAT_UNIT_WEEK
	case "month":
		return openpayv1.RepeatUnit_REPEAT_UNIT_MONTH
	case "year":
		return openpayv1.RepeatUnit_REPEAT_UNIT_YEAR
	default:
		return openpayv1.RepeatUnit_REPEAT_UNIT_UNSPECIFIED
	}
}

func protoRepeatUnitToDomain(u openpayv1.RepeatUnit) string {
	switch u {
	case openpayv1.RepeatUnit_REPEAT_UNIT_WEEK:
		return "week"
	case openpayv1.RepeatUnit_REPEAT_UNIT_MONTH:
		return "month"
	case openpayv1.RepeatUnit_REPEAT_UNIT_YEAR:
		return "year"
	default:
		return "month"
	}
}

// parseOpenPayDate parses an OpenPay date string ("YYYY-MM-DD") into a *time.Time.
// Returns nil on parse failure rather than crashing.
func parseOpenPayDate(s string) *time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil
	}
	return &t
}
