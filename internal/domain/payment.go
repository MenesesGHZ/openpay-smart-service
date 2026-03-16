package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// PaymentStatus mirrors OpenPay charge statuses.
type PaymentStatus string

const (
	PaymentStatusPending    PaymentStatus = "pending"
	PaymentStatusInProgress PaymentStatus = "in_progress"
	PaymentStatusCompleted  PaymentStatus = "completed"
	PaymentStatusFailed     PaymentStatus = "failed"
	PaymentStatusCancelled  PaymentStatus = "cancelled"
	PaymentStatusRefunded   PaymentStatus = "refunded"
	PaymentStatusChargeback PaymentStatus = "chargeback"
)

// PaymentMethod mirrors OpenPay charge methods.
type PaymentMethod string

const (
	PaymentMethodCard        PaymentMethod = "card"
	PaymentMethodBankAccount PaymentMethod = "bank_account" // SPEI
	PaymentMethodStore       PaymentMethod = "store"        // OXXO
)

// Payment is a concrete money movement record linked to an OpenPay charge.
//
// Fee accounting (all values in centavos / minor units):
//
//	GrossAmount  — what the member actually paid; equals the charge amount.
//	OpenpayFee   — OpenPay's processing fee as reported in charge.fee_details.
//	               Card: ~2.9% + $250 centavos; OXXO: $1000 centavos flat; SPEI: $0.
//	               This is deducted by OpenPay before crediting the merchant balance.
//	PlatformFee  — Service-owner fee on top: GrossAmount × tenant.PlatformFeeBPS / 10 000.
//	               Retained by the service owner; NOT forwarded to the tenant.
//	NetAmount    — What the tenant earns: GrossAmount − OpenpayFee − PlatformFee.
//	               This is the amount credited to the tenant's internal balance and
//	               subsequently sent via SPEI payout to their registered CLABE.
//
// Example for a $500.00 MXN card payment:
//
//	GrossAmount = 50000  ($500.00 MXN)
//	OpenpayFee  =  1700  ( $17.00 MXN — 2.9% + $2.50)
//	PlatformFee =   750  (  $7.50 MXN — 1.5%)
//	NetAmount   = 47550  ($475.50 MXN — credited to tenant balance)
type Payment struct {
	ID                   uuid.UUID       `db:"id"`
	TenantID             uuid.UUID       `db:"tenant_id"`
	MemberID             uuid.UUID       `db:"member_id"`
	LinkID               *uuid.UUID      `db:"link_id"`               // nil if not from a link
	OpenpayTransactionID string          `db:"openpay_transaction_id"` // OpenPay charge ID
	OrderID              string          `db:"order_id"`
	IdempotencyKey       string          `db:"idempotency_key"`

	// Fee breakdown — all in centavos (int64 minor units).
	// Set when the charge.succeeded webhook is processed; zero until then.
	GrossAmount  int64 `db:"gross_amount"`  // total charged to the member
	OpenpayFee   int64 `db:"openpay_fee"`   // from charge.fee_details (actual, not estimated)
	PlatformFee  int64 `db:"platform_fee"`  // GrossAmount × tenant.PlatformFeeBPS / 10 000
	NetAmount    int64 `db:"net_amount"`    // GrossAmount − OpenpayFee − PlatformFee

	Currency             string          `db:"currency"` // ISO 4217, e.g. "MXN"
	Method               PaymentMethod   `db:"method"`
	Status               PaymentStatus   `db:"status"`
	Description          string          `db:"description"`
	ErrorMessage         string          `db:"error_message"`
	ErrorCode            string          `db:"error_code"` // OpenPay numeric code, e.g. "3001"
	BankCLABE            string          `db:"bank_clabe"`
	BankName             string          `db:"bank_name"`
	BankReference        string          `db:"bank_reference"`
	BankAgreement        string          `db:"bank_agreement"`
	Metadata             json.RawMessage `db:"metadata"`
	CreatedAt            time.Time       `db:"created_at"`
	UpdatedAt            time.Time       `db:"updated_at"`
}

// NetAmountForCharge computes the net amount the tenant earns from a completed charge.
// grossCentavos is the charge amount; openpayFeeCentavos comes from charge.fee_details;
// platformFeeBPS is the tenant's configured basis points (e.g. 150 = 1.5%).
func NetAmountForCharge(grossCentavos, openpayFeeCentavos int64, platformFeeBPS int) (platformFee, netAmount int64) {
	platformFee = grossCentavos * int64(platformFeeBPS) / 10_000
	netAmount = grossCentavos - openpayFeeCentavos - platformFee
	return platformFee, netAmount
}

// Payout represents a disbursement to the tenant, mapped to an OpenPay payout.
type Payout struct {
	ID                   uuid.UUID   `db:"id"`
	TenantID             uuid.UUID   `db:"tenant_id"`
	OpenpayTransactionID string      `db:"openpay_transaction_id"`
	Amount               int64       `db:"amount"`
	Currency             string      `db:"currency"`
	Status               PayoutStatus `db:"status"`
	Description          string      `db:"description"`
	ScheduledFor         *time.Time  `db:"scheduled_for"`
	CreatedAt            time.Time   `db:"created_at"`
	UpdatedAt            time.Time   `db:"updated_at"`
}

// PayoutStatus mirrors OpenPay payout statuses.
type PayoutStatus string

const (
	PayoutStatusPending    PayoutStatus = "pending"
	PayoutStatusInProgress PayoutStatus = "in_progress"
	PayoutStatusCompleted  PayoutStatus = "completed"
	PayoutStatusFailed     PayoutStatus = "failed"
)

// PaymentEvent is a domain event emitted to Kafka when a payment changes state.
type PaymentEvent struct {
	EventID   string        `json:"event_id"`
	PaymentID string        `json:"payment_id"`
	TenantID  string        `json:"tenant_id"`
	MemberID  string        `json:"member_id"`
	Status    PaymentStatus `json:"status"`
	EventType string        `json:"event_type"` // e.g. "charge.succeeded"
	OccurredAt time.Time   `json:"occurred_at"`
}
