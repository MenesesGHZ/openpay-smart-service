// Package repository defines the storage interfaces used by the domain layer.
// Implementations live in the postgres sub-package.
package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/menesesghz/openpay-smart-service/internal/domain"
)

// ─── Tenant ──────────────────────────────────────────────────────────────────

type TenantRepository interface {
	Create(ctx context.Context, t *domain.Tenant) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Tenant, error)
	GetByAPIKeyHash(ctx context.Context, hash string) (*domain.Tenant, error)
	Update(ctx context.Context, t *domain.Tenant) error
	// List returns active (non-deleted) tenants with optional tier filter and cursor pagination.
	List(ctx context.Context, opts ListTenantsOptions) ([]*domain.Tenant, string, error)
	// Delete soft-deletes a tenant by setting deleted_at = NOW().
	// A deleted tenant cannot authenticate and is excluded from List results.
	Delete(ctx context.Context, id uuid.UUID) error
	// RotateAPIKey replaces the tenant's api_key_hash and api_key_prefix atomically.
	RotateAPIKey(ctx context.Context, id uuid.UUID, newHash, newPrefix string) error

	// Logo — optional tenant branding stored in S3.
	// SetLogoURL persists the S3 public URL for the tenant's logo.
	SetLogoURL(ctx context.Context, tenantID uuid.UUID, logoURL string) error

	// Bank account — payout destination for this tenant via SPEI.
	// The CLABE is stored AES-encrypted; only a masked version is returned to callers.
	SetBankAccount(ctx context.Context, tenantID uuid.UUID, clabe, holderName, bankName string) error
	ClearBankAccount(ctx context.Context, tenantID uuid.UUID) error

	// Disbursement schedule
	UpsertSchedule(ctx context.Context, s *domain.DisbursementSchedule) error
	GetSchedule(ctx context.Context, tenantID uuid.UUID) (*domain.DisbursementSchedule, error)
	// ListDueSchedules returns all enabled schedules whose next_run_at <= before.
	// Used by the scheduler worker to find tenants to disburse.
	ListDueSchedules(ctx context.Context, before time.Time) ([]*domain.DisbursementSchedule, error)
}

type ListTenantsOptions struct {
	Tier      string // optional filter: "free" | "standard" | "enterprise"
	PageSize  int    // 0 → default (20)
	PageToken string // opaque cursor from previous ListTenantsResponse
}

// ─── Member ───────────────────────────────────────────────────────────────────

type MemberRepository interface {
	Create(ctx context.Context, m *domain.Member) error
	GetByID(ctx context.Context, tenantID, memberID uuid.UUID) (*domain.Member, error)
	GetByExternalID(ctx context.Context, tenantID uuid.UUID, externalID string) (*domain.Member, error)
	Update(ctx context.Context, m *domain.Member) error
	List(ctx context.Context, opts ListMembersOptions) ([]*domain.Member, string, error) // returns next cursor

	// Payment links
	CreatePaymentLink(ctx context.Context, l *domain.PaymentLink) error
	GetPaymentLinkByToken(ctx context.Context, token string) (*domain.PaymentLink, error)
	GetPaymentLinkByID(ctx context.Context, tenantID, linkID uuid.UUID) (*domain.PaymentLink, error)
	UpdatePaymentLink(ctx context.Context, l *domain.PaymentLink) error
	ListPaymentLinks(ctx context.Context, opts ListPaymentLinksOptions) ([]*domain.PaymentLink, string, error)

	// Card management
	CreateCard(ctx context.Context, card *domain.MemberCard) error
	GetCardByID(ctx context.Context, tenantID, memberID, cardID uuid.UUID) (*domain.MemberCard, error)
	ListCards(ctx context.Context, tenantID, memberID uuid.UUID) ([]*domain.MemberCard, error)
	DeleteCard(ctx context.Context, tenantID, memberID, cardID uuid.UUID) error

	// Subscription links
	CreateSubscriptionLink(ctx context.Context, link *domain.SubscriptionLink) error
	GetSubscriptionLinkByToken(ctx context.Context, token string) (*domain.SubscriptionLink, error)
	GetSubscriptionLinkByID(ctx context.Context, tenantID, linkID uuid.UUID) (*domain.SubscriptionLink, error)
	UpdateSubscriptionLink(ctx context.Context, link *domain.SubscriptionLink) error
}

type ListMembersOptions struct {
	TenantID      uuid.UUID
	Email         string
	KYCStatus     string
	CreatedAfter  *time.Time
	CreatedBefore *time.Time
	PageSize      int
	PageToken     string // cursor
}

type ListPaymentLinksOptions struct {
	TenantID  uuid.UUID
	MemberID  *uuid.UUID
	Status    string
	PageSize  int
	PageToken string
}

// ─── Payment ─────────────────────────────────────────────────────────────────

// PaymentFees carries the computed fee breakdown for settling a completed charge.
// All values are in centavos (int64 minor units).
type PaymentFees struct {
	GrossAmount int64
	OpenpayFee  int64
	PlatformFee int64
	NetAmount   int64
}

type PaymentRepository interface {
	Create(ctx context.Context, p *domain.Payment) error
	GetByID(ctx context.Context, tenantID, paymentID uuid.UUID) (*domain.Payment, error)
	GetByOpenpayTransactionID(ctx context.Context, txID string) (*domain.Payment, error)
	GetByIdempotencyKey(ctx context.Context, tenantID uuid.UUID, key string) (*domain.Payment, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status domain.PaymentStatus, openpayErr ...string) error

	// SettlePayment atomically marks a payment as completed and writes the full
	// fee breakdown in a single UPDATE.  Called exclusively from the
	// charge.succeeded webhook handler — never call UpdateStatus for settlements.
	SettlePayment(ctx context.Context, id uuid.UUID, fees PaymentFees) error

	List(ctx context.Context, opts ListPaymentsOptions) ([]*domain.Payment, string, error)

	// Payout
	CreatePayout(ctx context.Context, p *domain.Payout) error
	GetPayoutByID(ctx context.Context, tenantID, payoutID uuid.UUID) (*domain.Payout, error)
	UpdatePayoutStatus(ctx context.Context, id uuid.UUID, status domain.PayoutStatus) error
}

type ListPaymentsOptions struct {
	TenantID       uuid.UUID
	MemberID       *uuid.UUID
	SubscriptionID *uuid.UUID // filter to a specific subscription's payments
	Statuses       []string
	Methods        []string
	Currency       string
	AmountMin      *int64
	AmountMax      *int64
	OrderID        string
	From           *time.Time
	To             *time.Time
	PageSize       int
	PageToken      string
}

// ─── Plan ────────────────────────────────────────────────────────────────────

type PlanRepository interface {
	Create(ctx context.Context, p *domain.Plan) error
	GetByID(ctx context.Context, tenantID, planID uuid.UUID) (*domain.Plan, error)
	GetByOpenpayPlanID(ctx context.Context, openpayPlanID string) (*domain.Plan, error)
	List(ctx context.Context, opts ListPlansOptions) ([]*domain.Plan, string, error)
	Deactivate(ctx context.Context, tenantID, planID uuid.UUID) error
}

type ListPlansOptions struct {
	TenantID   uuid.UUID
	ActiveOnly bool
	PageSize   int
	PageToken  string
}

// ─── Subscription ─────────────────────────────────────────────────────────────

type SubscriptionRepository interface {
	Create(ctx context.Context, s *domain.Subscription) error
	GetByID(ctx context.Context, tenantID, subID uuid.UUID) (*domain.Subscription, error)
	GetByOpenpaySubID(ctx context.Context, openpaySubID string) (*domain.Subscription, error)
	List(ctx context.Context, opts ListSubscriptionsOptions) ([]*domain.Subscription, string, error)
	// UpdateStatus sets the subscription status and bumps updated_at.
	UpdateStatus(ctx context.Context, id uuid.UUID, status domain.SubscriptionStatus) error
	// RecordCharge updates last_charge_id and resets failed_charge_count to 0.
	// Called when subscription.charge.succeeded is received.
	RecordCharge(ctx context.Context, id uuid.UUID, chargePaymentID uuid.UUID) error
	// IncrementFailedCharge bumps failed_charge_count and, if it reaches the plan's
	// retry_times, transitions status to the plan's status_on_retry_end.
	// Called when subscription.charge.failed is received.
	IncrementFailedCharge(ctx context.Context, id uuid.UUID) error
	// SetCancelAtPeriodEnd marks cancel_at_period_end = true.
	// The subscription remains active until period_end_date, then becomes cancelled.
	SetCancelAtPeriodEnd(ctx context.Context, id uuid.UUID) error
}

type ListSubscriptionsOptions struct {
	TenantID  uuid.UUID
	MemberID  *uuid.UUID
	PlanID    *uuid.UUID
	Statuses  []string
	PageSize  int
	PageToken string
}

// ─── Balance ─────────────────────────────────────────────────────────────────

type BalanceRepository interface {
	Upsert(ctx context.Context, b *domain.Balance) error
	GetTenantBalance(ctx context.Context, tenantID uuid.UUID, currency string) (*domain.Balance, error)
	GetMemberBalance(ctx context.Context, tenantID, memberID uuid.UUID, currency string) (*domain.Balance, error)
	List(ctx context.Context, tenantID uuid.UUID, currency string, pageSize int, pageToken string) ([]*domain.Balance, string, error)
	GetHistory(ctx context.Context, tenantID, memberID uuid.UUID, currency, granularity string, from, to time.Time) ([]*domain.BalanceSnapshot, error)

	// AddPending increases the pending balance when a new charge is initiated.
	// Called when a payment record is first created (status = pending / in_progress).
	AddPending(ctx context.Context, tenantID uuid.UUID, amount int64, currency string) error

	// CreditSettlement moves funds from pending to available after a charge.succeeded
	// webhook is processed.  grossAmount is subtracted from pending; netAmount is
	// added to available.  The difference (openpay_fee + platform_fee) is simply
	// removed — it was never part of the tenant's balance.
	// This must execute as a single atomic UPDATE to avoid race conditions.
	CreditSettlement(ctx context.Context, tenantID uuid.UUID, grossAmount, netAmount int64, currency string) error

	// DebitAvailable reduces the available balance when a payout is dispatched.
	// Called by the DisbursementScheduler before calling POST /payouts on OpenPay.
	DebitAvailable(ctx context.Context, tenantID uuid.UUID, amount int64, currency string) error
}

// ─── Webhook ──────────────────────────────────────────────────────────────────

type WebhookRepository interface {
	CreateSubscription(ctx context.Context, s *domain.WebhookSubscription) error
	GetSubscription(ctx context.Context, tenantID, subID uuid.UUID) (*domain.WebhookSubscription, error)
	UpdateSubscription(ctx context.Context, s *domain.WebhookSubscription) error
	DeleteSubscription(ctx context.Context, tenantID, subID uuid.UUID) error
	ListSubscriptions(ctx context.Context, tenantID uuid.UUID, enabledOnly bool, pageSize int, pageToken string) ([]*domain.WebhookSubscription, string, error)
	// MatchingSubscriptions returns subscriptions that listen to the given event type.
	MatchingSubscriptions(ctx context.Context, tenantID uuid.UUID, eventType string) ([]*domain.WebhookSubscription, error)

	CreateDelivery(ctx context.Context, d *domain.WebhookDelivery) error
	GetDelivery(ctx context.Context, deliveryID uuid.UUID) (*domain.WebhookDelivery, error)
	UpdateDelivery(ctx context.Context, d *domain.WebhookDelivery) error
	ListPendingDeliveries(ctx context.Context, limit int) ([]*domain.WebhookDelivery, error)
}

// ─── Audit ────────────────────────────────────────────────────────────────────

type AuditRepository interface {
	Log(ctx context.Context, entry *AuditEntry) error
}

type AuditEntry struct {
	TenantID     uuid.UUID
	Actor        string // API key prefix or "system"
	Operation    string // e.g. "CreateMember", "UpdatePaymentStatus"
	ResourceType string // e.g. "Member", "Payment"
	ResourceID   string
	Payload      []byte // JSON
}
