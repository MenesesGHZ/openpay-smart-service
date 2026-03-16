package domain

import (
	"time"

	"github.com/google/uuid"
)

// KYCStatus represents a member's identity verification state.
type KYCStatus string

const (
	KYCStatusPending  KYCStatus = "pending"
	KYCStatusVerified KYCStatus = "verified"
	KYCStatusRejected KYCStatus = "rejected"
)

// Member is an end-user linked to a tenant. Maps to an OpenPay Customer.
type Member struct {
	ID                 uuid.UUID `db:"id"`
	TenantID           uuid.UUID `db:"tenant_id"`
	OpenpayCustomerID  string    `db:"openpay_customer_id"` // returned by POST /customers
	ExternalID         string    `db:"external_id"`         // caller-supplied reference
	Name               string    `db:"name"`
	Email              string    `db:"email"`
	Phone              string    `db:"phone"`
	KYCStatus          KYCStatus `db:"kyc_status"`
	AddressLine1       string    `db:"address_line1"`
	AddressLine2       string    `db:"address_line2"`
	AddressCity        string    `db:"address_city"`
	AddressState       string    `db:"address_state"`
	AddressPostalCode  string    `db:"address_postal_code"`
	AddressCountry     string    `db:"address_country"`
	CreatedAt          time.Time `db:"created_at"`
	UpdatedAt          time.Time `db:"updated_at"`
}

// PaymentLinkStatus represents the lifecycle state of a PaymentLink.
type PaymentLinkStatus string

const (
	PaymentLinkStatusActive    PaymentLinkStatus = "active"
	PaymentLinkStatusRedeemed  PaymentLinkStatus = "redeemed"
	PaymentLinkStatusExpired   PaymentLinkStatus = "expired"
	PaymentLinkStatusCancelled PaymentLinkStatus = "cancelled"
)

// PaymentLink is a shareable token a tenant creates for a member to initiate payment.
// It does not correspond directly to an OpenPay resource; the charge is created
// when the link is redeemed.
type PaymentLink struct {
	ID          uuid.UUID         `db:"id"`
	TenantID    uuid.UUID         `db:"tenant_id"`
	MemberID    uuid.UUID         `db:"member_id"`
	Token       string            `db:"token"`       // URL-safe random token
	Amount      int64             `db:"amount"`      // minor currency units
	Currency    string            `db:"currency"`    // ISO 4217 (e.g. "MXN")
	Description string            `db:"description"`
	OrderID     string            `db:"order_id"`
	Status      PaymentLinkStatus `db:"status"`
	ExpiresAt   *time.Time        `db:"expires_at"`  // nil = no expiry
	RedeemedAt  *time.Time        `db:"redeemed_at"`
	CreatedAt   time.Time         `db:"created_at"`
}
