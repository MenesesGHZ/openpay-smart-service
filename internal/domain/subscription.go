package domain

import (
	"time"

	"github.com/google/uuid"
)

// ─── Plan ────────────────────────────────────────────────────────────────────

// Plan represents a recurring billing plan owned by a tenant.
// It maps 1:1 to an OpenPay Plan object (POST /v1/{merchantId}/plans).
//
// Since the service runs on a single OpenPay merchant account, all plans
// live under that account. The tenant_id field enforces logical ownership so
// tenants can only see and use their own plans.
type Plan struct {
	ID              uuid.UUID `db:"id"`
	TenantID        uuid.UUID `db:"tenant_id"`
	OpenpayPlanID   string    `db:"openpay_plan_id"` // OpenPay's plan ID, e.g. "pld8f2sbc..."
	Name            string    `db:"name"`
	Amount          int64     `db:"amount"`       // recurring charge in centavos
	Currency        string    `db:"currency"`     // ISO 4217, e.g. "MXN"
	RepeatEvery     int       `db:"repeat_every"` // e.g. 1
	RepeatUnit      string    `db:"repeat_unit"`  // "week" | "month" | "year"
	TrialDays       int       `db:"trial_days"`   // 0 = no trial
	RetryTimes      int       `db:"retry_times"`  // how many times to retry on failure before giving up
	StatusOnRetryEnd string   `db:"status_on_retry_end"` // "cancelled" | "unpaid"
	Active          bool      `db:"active"`
	CreatedAt       time.Time `db:"created_at"`
	UpdatedAt       time.Time `db:"updated_at"`
}

// ─── Subscription ────────────────────────────────────────────────────────────

// SubscriptionStatus mirrors OpenPay subscription statuses.
type SubscriptionStatus string

const (
	SubscriptionStatusTrial     SubscriptionStatus = "trial"
	SubscriptionStatusActive    SubscriptionStatus = "active"
	SubscriptionStatusPastDue   SubscriptionStatus = "past_due"   // charge failed, retrying
	SubscriptionStatusCancelled SubscriptionStatus = "cancelled"
	SubscriptionStatusExpired   SubscriptionStatus = "expired"
	SubscriptionStatusUnpaid    SubscriptionStatus = "unpaid" // retries exhausted, plan set to "unpaid"
)

// Subscription links a member to a recurring billing plan.
// It maps 1:1 to an OpenPay Subscription object:
//
//	POST   /v1/{merchantId}/customers/{customerId}/subscriptions
//	GET    /v1/{merchantId}/customers/{customerId}/subscriptions/{subId}
//	DELETE /v1/{merchantId}/customers/{customerId}/subscriptions/{subId}
//
// OpenPay charges the member's stored card automatically at each billing cycle.
// The webhook events subscription.charge.succeeded and subscription.charge.failed
// fire when the automatic charge attempts complete or fail.
type Subscription struct {
	ID                 uuid.UUID          `db:"id"`
	TenantID           uuid.UUID          `db:"tenant_id"`
	MemberID           uuid.UUID          `db:"member_id"`
	PlanID             uuid.UUID          `db:"plan_id"`
	OpenpaySubID       string             `db:"openpay_sub_id"`  // OpenPay subscription ID
	SourceCardID       string             `db:"source_card_id"` // OpenPay card ID used for billing
	Status             SubscriptionStatus `db:"status"`
	TrialEndDate       *time.Time         `db:"trial_end_date"`   // nil if no trial
	PeriodEndDate      *time.Time         `db:"period_end_date"`  // end of current billing period
	CancelAtPeriodEnd  bool               `db:"cancel_at_period_end"`
	FailedChargeCount  int                `db:"failed_charge_count"` // incremented by webhook; reset on success
	LastChargeID       *uuid.UUID         `db:"last_charge_id"`      // most recent Payment.ID
	CreatedAt          time.Time          `db:"created_at"`
	UpdatedAt          time.Time          `db:"updated_at"`
}

// SubscriptionEvent is a domain event emitted to Kafka when a subscription changes state.
type SubscriptionEvent struct {
	EventID        string             `json:"event_id"`
	SubscriptionID string             `json:"subscription_id"`
	TenantID       string             `json:"tenant_id"`
	MemberID       string             `json:"member_id"`
	PlanID         string             `json:"plan_id"`
	Status         SubscriptionStatus `json:"status"`
	EventType      string             `json:"event_type"` // e.g. "subscription.charge.succeeded"
	PaymentID      string             `json:"payment_id,omitempty"`
	OccurredAt     time.Time          `json:"occurred_at"`
}
