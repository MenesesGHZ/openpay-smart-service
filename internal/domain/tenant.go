package domain

import (
	"time"

	"github.com/google/uuid"
)

// Tenant represents a platform operator (your customer) who uses this service
// to collect payments from their own end-users (Members).
//
// Architecture note: there is ONE OpenPay merchant account — owned by the
// service operator (you). All customer payments flow into that single merchant
// balance. This service tracks which funds belong to which tenant, and the
// DisbursementScheduler sends the right amounts to each tenant's bank account
// via POST /v1/{merchantId}/payouts → SPEI transfer to tenant CLABE.
//
// OpenPay credentials (merchant_id, private_key) are service-level config,
// NOT stored per tenant. See internal/config/config.go → OpenPayConfig.
type Tenant struct {
	ID             uuid.UUID `db:"id"`
	Name           string    `db:"name"`
	APIKeyHash     string    `db:"api_key_hash"` // SHA-256 of the tenant's API key
	Tier           string    `db:"tier"`         // "free" | "standard" | "enterprise"

	// Payout destination — where this tenant receives their disbursements via SPEI.
	// Set by the tenant via SetupBankAccount RPC; required before any payout can run.
	BankCLABE      string `db:"bank_clabe"`       // 18-digit CLABE
	BankHolderName string `db:"bank_holder_name"` // account holder full name
	BankName       string `db:"bank_name"`        // e.g. "BBVA", "BANAMEX", "SANTANDER"

	// PlatformFeeBPS is the service-owner's fee applied on top of the OpenPay fee,
	// expressed as basis points (1 BPS = 0.01%).  150 BPS = 1.5%.
	// Applied at charge.succeeded time: platform_fee = gross_amount × PlatformFeeBPS / 10 000.
	// Defaults to the service-wide default (e.g. 150) if not explicitly set per tenant.
	PlatformFeeBPS int `db:"platform_fee_bps"`

	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// HasBankAccount returns true when the tenant has a complete payout destination set up.
func (t *Tenant) HasBankAccount() bool {
	return t.BankCLABE != "" && t.BankHolderName != ""
}

// DisbursementSchedule defines when the scheduler sweeps this tenant's accumulated
// balance from the OpenPay merchant account and fires a payout to their CLABE.
type DisbursementSchedule struct {
	TenantID  uuid.UUID `db:"tenant_id"`
	Frequency string    `db:"frequency"` // daily | weekly | monthly | custom
	CronExpr  string    `db:"cron_expr"` // populated when frequency = "custom"
	Enabled   bool      `db:"enabled"`
	NextRunAt time.Time `db:"next_run_at"`
	LastRunAt time.Time `db:"last_run_at"`
	UpdatedAt time.Time `db:"updated_at"`
}
