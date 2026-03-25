package domain

import (
	"time"

	"github.com/google/uuid"
)

// FeeType determines how the platform fee is applied relative to OpenPay's fee.
type FeeType string

const (
	// FeeTypeAdded means the computed platform fee is added on top of OpenPay's
	// fee. The tenant absorbs both.
	//   platform_fee = (gross × percentage_bps / 10 000) + fixed_centavos
	//   net          = gross − openpay_fee − platform_fee
	FeeTypeAdded FeeType = "added"

	// FeeTypeInclusive means the Fee represents the total constant fee shown to
	// the tenant (OpenPay + platform combined). The platform takes whatever is
	// left after OpenPay's cut — which can be negative when OpenPay's fee exceeds
	// the constant.
	//   constant     = (gross × percentage_bps / 10 000) + fixed_centavos
	//   platform_fee = constant − openpay_fee  (may be negative)
	//   net          = gross − constant
	FeeTypeInclusive FeeType = "inclusive"
)

// PlatformFeeConfig defines the fee the platform retains per charge.
// At least one of PercentageBPS or FixedCentavos must be greater than 0.
type PlatformFeeConfig struct {
	// PercentageBPS is the percentage of the gross amount in basis points
	// (e.g. 150 = 1.5%). 0 means no percentage component.
	PercentageBPS int `db:"platform_fee_percentage_bps"`

	// FixedCentavos is a flat amount in centavos (e.g. 250 = $2.50 MXN).
	// 0 means no fixed component.
	FixedCentavos int64 `db:"platform_fee_fixed_centavos"`
}

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
	APIKeyHash     string    `db:"api_key_hash"`   // SHA-256 of the tenant's API key
	APIKeyPrefix   string    `db:"api_key_prefix"` // first 12 chars of raw key, for display only
	Tier           string    `db:"tier"`           // "free" | "standard" | "enterprise"

	// Payout destination — where this tenant receives their disbursements via SPEI.
	// Set by the tenant via SetupBankAccount RPC; required before any payout can run.
	BankCLABE      string `db:"bank_clabe"`       // 18-digit CLABE
	BankHolderName string `db:"bank_holder_name"` // account holder full name
	BankName       string `db:"bank_name"`        // e.g. "BBVA", "BANAMEX", "SANTANDER"

	// PlatformFee is the fee structure the platform retains from each charge.
	PlatformFee PlatformFeeConfig `db:"platform_fee"`

	// FeeType controls how PlatformFee is interpreted relative to OpenPay's fee.
	FeeType FeeType `db:"fee_type"`

	// LogoURL is the publicly accessible URL of the tenant's logo stored in S3.
	// Empty string means no logo has been uploaded yet.
	LogoURL string `db:"logo_url"`

	// CardNetworksEnabled gates the card network deny-list. When false, all
	// card brands are accepted regardless of CardNetworkList.
	CardNetworksEnabled bool `db:"card_networks_enabled"`

	// CardNetworkList holds the denied card network brands (lower-case), e.g.
	// ["american_express", "visa"]. Only enforced when CardNetworksEnabled is true.
	CardNetworkList []string `db:"card_network_list"`

	CreatedAt time.Time  `db:"created_at"`
	UpdatedAt time.Time  `db:"updated_at"`
	DeletedAt *time.Time `db:"deleted_at"` // nil = active; non-nil = soft-deleted
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
