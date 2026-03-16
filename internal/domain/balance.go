package domain

import (
	"time"

	"github.com/google/uuid"
)

// Balance is an aggregated view of a tenant's or member's funds.
type Balance struct {
	ID        uuid.UUID `db:"id"`
	TenantID  uuid.UUID `db:"tenant_id"`
	MemberID  *uuid.UUID `db:"member_id"` // nil = tenant-level balance
	Available int64     `db:"available"` // settled funds ready for disbursement (minor units)
	Pending   int64     `db:"pending"`   // charges in-flight not yet settled
	Currency  string    `db:"currency"`
	UpdatedAt time.Time `db:"updated_at"`
}

// BalanceSnapshot is a point-in-time reading used for history charts.
type BalanceSnapshot struct {
	At        time.Time `db:"at"`
	Available int64     `db:"available"`
	Pending   int64     `db:"pending"`
	Currency  string    `db:"currency"`
}
