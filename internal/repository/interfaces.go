// Package repository defines the storage interfaces used by the domain layer.
// Implementations live in the postgres sub-package.
package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/your-org/openpay-smart-service/internal/domain"
)

// ─── Tenant ──────────────────────────────────────────────────────────────────

type TenantRepository interface {
	Create(ctx context.Context, t *domain.Tenant) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Tenant, error)
	GetByAPIKeyHash(ctx context.Context, hash string) (*domain.Tenant, error)
	Update(ctx context.Context, t *domain.Tenant) error

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

type PaymentRepository interface {
	Create(ctx context.Context, p *domain.Payment) error
	GetByID(ctx context.Context, tenantID, paymentID uuid.UUID) (*domain.Payment, error)
	GetByOpenpayTransactionID(ctx context.Context, txID string) (*domain.Payment, error)
	GetByIdempotencyKey(ctx context.Context, tenantID uuid.UUID, key string) (*domain.Payment, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status domain.PaymentStatus, openpayErr ...string) error
	List(ctx context.Context, opts ListPaymentsOptions) ([]*domain.Payment, string, error)

	// Payout
	CreatePayout(ctx context.Context, p *domain.Payout) error
	GetPayoutByID(ctx context.Context, tenantID, payoutID uuid.UUID) (*domain.Payout, error)
	UpdatePayoutStatus(ctx context.Context, id uuid.UUID, status domain.PayoutStatus) error
}

type ListPaymentsOptions struct {
	TenantID      uuid.UUID
	MemberID      *uuid.UUID
	Statuses      []string
	Methods       []string
	Currency      string
	AmountMin     *int64
	AmountMax     *int64
	OrderID       string
	From          *time.Time
	To            *time.Time
	PageSize      int
	PageToken     string
}

// ─── Balance ─────────────────────────────────────────────────────────────────

type BalanceRepository interface {
	Upsert(ctx context.Context, b *domain.Balance) error
	GetTenantBalance(ctx context.Context, tenantID uuid.UUID, currency string) (*domain.Balance, error)
	GetMemberBalance(ctx context.Context, tenantID, memberID uuid.UUID, currency string) (*domain.Balance, error)
	List(ctx context.Context, tenantID uuid.UUID, currency string, pageSize int, pageToken string) ([]*domain.Balance, string, error)
	GetHistory(ctx context.Context, tenantID, memberID uuid.UUID, currency, granularity string, from, to time.Time) ([]*domain.BalanceSnapshot, error)
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
