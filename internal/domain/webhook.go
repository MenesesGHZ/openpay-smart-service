package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// DeliveryStatus tracks the outbound webhook lifecycle.
type DeliveryStatus string

const (
	DeliveryStatusPending   DeliveryStatus = "pending"
	DeliveryStatusDelivered DeliveryStatus = "delivered"
	DeliveryStatusFailed    DeliveryStatus = "failed"
	DeliveryStatusDLQ       DeliveryStatus = "dlq" // exhausted all retries
)

// RetryPolicy defines how a webhook subscription retries failed deliveries.
type RetryPolicy struct {
	MaxAttempts   int   `json:"max_attempts"`
	IntervalsSec  []int `json:"intervals_sec"` // per-attempt delay; len must equal MaxAttempts
}

// DefaultRetryPolicy is applied when no policy is specified.
var DefaultRetryPolicy = RetryPolicy{
	MaxAttempts:  7,
	IntervalsSec: []int{5, 30, 120, 600, 3600, 21600, 86400},
}

// WebhookSubscription is a tenant's registered callback endpoint.
type WebhookSubscription struct {
	ID          uuid.UUID   `db:"id"`
	TenantID    uuid.UUID   `db:"tenant_id"`
	URL         string      `db:"url"`
	SecretEnc   string      `db:"secret_enc"` // AES-256-GCM encrypted HMAC secret
	Events      []string    `db:"events"`     // event type strings; ["*"] = all
	RetryPolicy RetryPolicy `db:"retry_policy"`
	Enabled     bool        `db:"enabled"`
	CreatedAt   time.Time   `db:"created_at"`
	UpdatedAt   time.Time   `db:"updated_at"`
}

// WebhookDelivery is a durable record of a single outbound delivery attempt.
type WebhookDelivery struct {
	ID             uuid.UUID      `db:"id"`
	SubscriptionID uuid.UUID      `db:"subscription_id"`
	EventType      string         `db:"event_type"`
	Payload        json.RawMessage `db:"payload"`
	Status         DeliveryStatus `db:"status"`
	Attempts       int            `db:"attempts"`
	ResponseCode   int            `db:"response_code"`
	LatencyMS      int64          `db:"latency_ms"`
	ErrorMessage   string         `db:"error_message"`
	CreatedAt      time.Time      `db:"created_at"`
	LastAttemptedAt *time.Time    `db:"last_attempted_at"`
	NextRetryAt    *time.Time     `db:"next_retry_at"`
}

// OpenPayEvent is the raw payload received from OpenPay on the ingress endpoint.
// Ref: https://documents.openpay.mx/en/docs/webhooks.html
type OpenPayEvent struct {
	Type            string          `json:"type"`
	EventDate       string          `json:"event_date"`
	VerificationCode string         `json:"verification_code,omitempty"`
	Transaction     json.RawMessage `json:"transaction,omitempty"`
}
