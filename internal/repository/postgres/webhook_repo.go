package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/menesesghz/openpay-smart-service/internal/domain"
)

// WebhookRepo implements repository.WebhookRepository backed by PostgreSQL.
type WebhookRepo struct{ db *pgxpool.Pool }

func NewWebhookRepo(db *pgxpool.Pool) *WebhookRepo { return &WebhookRepo{db: db} }

// ── WebhookSubscription ───────────────────────────────────────────────────────

const webhookSubCols = `id, tenant_id, url, secret_enc, events, retry_policy, enabled, created_at, updated_at`

func (r *WebhookRepo) scanSub(row pgx.Row) (*domain.WebhookSubscription, error) {
	var w domain.WebhookSubscription
	var policyJSON []byte
	err := row.Scan(
		&w.ID, &w.TenantID, &w.URL, &w.SecretEnc,
		&w.Events, &policyJSON, &w.Enabled,
		&w.CreatedAt, &w.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scan webhook sub: %w", err)
	}
	if err := json.Unmarshal(policyJSON, &w.RetryPolicy); err != nil {
		return nil, fmt.Errorf("unmarshal retry policy: %w", err)
	}
	return &w, nil
}

func (r *WebhookRepo) scanSubRow(rows pgx.Rows) (*domain.WebhookSubscription, error) {
	var w domain.WebhookSubscription
	var policyJSON []byte
	err := rows.Scan(
		&w.ID, &w.TenantID, &w.URL, &w.SecretEnc,
		&w.Events, &policyJSON, &w.Enabled,
		&w.CreatedAt, &w.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan webhook sub row: %w", err)
	}
	if err := json.Unmarshal(policyJSON, &w.RetryPolicy); err != nil {
		return nil, fmt.Errorf("unmarshal retry policy: %w", err)
	}
	return &w, nil
}

func (r *WebhookRepo) CreateSubscription(ctx context.Context, s *domain.WebhookSubscription) error {
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	now := time.Now()
	s.CreatedAt = now
	s.UpdatedAt = now

	policyJSON, err := json.Marshal(s.RetryPolicy)
	if err != nil {
		return fmt.Errorf("marshal retry policy: %w", err)
	}

	_, err = r.db.Exec(ctx, `
		INSERT INTO webhook_subscriptions
		    (id, tenant_id, url, secret_enc, events, retry_policy, enabled, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		s.ID, s.TenantID, s.URL, s.SecretEnc, s.Events, policyJSON, s.Enabled, s.CreatedAt, s.UpdatedAt,
	)
	return err
}

func (r *WebhookRepo) GetSubscription(ctx context.Context, tenantID, subID uuid.UUID) (*domain.WebhookSubscription, error) {
	row := r.db.QueryRow(ctx,
		`SELECT `+webhookSubCols+` FROM webhook_subscriptions WHERE id = $1 AND tenant_id = $2`,
		subID, tenantID,
	)
	return r.scanSub(row)
}

func (r *WebhookRepo) UpdateSubscription(ctx context.Context, s *domain.WebhookSubscription) error {
	s.UpdatedAt = time.Now()
	policyJSON, err := json.Marshal(s.RetryPolicy)
	if err != nil {
		return fmt.Errorf("marshal retry policy: %w", err)
	}
	tag, err := r.db.Exec(ctx, `
		UPDATE webhook_subscriptions
		SET url = $1, secret_enc = $2, events = $3, retry_policy = $4, enabled = $5, updated_at = $6
		WHERE id = $7 AND tenant_id = $8`,
		s.URL, s.SecretEnc, s.Events, policyJSON, s.Enabled, s.UpdatedAt, s.ID, s.TenantID,
	)
	if err != nil {
		return fmt.Errorf("update webhook sub: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *WebhookRepo) DeleteSubscription(ctx context.Context, tenantID, subID uuid.UUID) error {
	tag, err := r.db.Exec(ctx,
		`DELETE FROM webhook_subscriptions WHERE id = $1 AND tenant_id = $2`,
		subID, tenantID,
	)
	if err != nil {
		return fmt.Errorf("delete webhook sub: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *WebhookRepo) ListSubscriptions(ctx context.Context, tenantID uuid.UUID, enabledOnly bool, pageSize int, pageToken string) ([]*domain.WebhookSubscription, string, error) {
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	offset := decodePageToken(pageToken)

	q := `SELECT ` + webhookSubCols + ` FROM webhook_subscriptions WHERE tenant_id = $1`
	args := []any{tenantID}
	if enabledOnly {
		q += ` AND enabled = TRUE`
	}
	q += fmt.Sprintf(` ORDER BY created_at DESC LIMIT %d OFFSET %d`, pageSize+1, offset)

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list webhook subs: %w", err)
	}
	defer rows.Close()

	var result []*domain.WebhookSubscription
	for rows.Next() {
		w, err := r.scanSubRow(rows)
		if err != nil {
			return nil, "", err
		}
		result = append(result, w)
	}

	nextToken := ""
	if len(result) > pageSize {
		result = result[:pageSize]
		nextToken = encodePageToken(offset + pageSize)
	}
	return result, nextToken, nil
}

func (r *WebhookRepo) MatchingSubscriptions(ctx context.Context, tenantID uuid.UUID, eventType string) ([]*domain.WebhookSubscription, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+webhookSubCols+`
		FROM webhook_subscriptions
		WHERE tenant_id = $1 AND enabled = TRUE
		  AND (events @> ARRAY['*']::TEXT[] OR events @> ARRAY[$2]::TEXT[])`,
		tenantID, eventType,
	)
	if err != nil {
		return nil, fmt.Errorf("matching webhook subs: %w", err)
	}
	defer rows.Close()

	var result []*domain.WebhookSubscription
	for rows.Next() {
		w, err := r.scanSubRow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, w)
	}
	return result, nil
}

// ── WebhookDelivery ───────────────────────────────────────────────────────────

const deliveryCols = `id, subscription_id, event_type, payload, status, attempts,
	response_code, latency_ms, error_message, created_at, last_attempted_at, next_retry_at`

func scanDelivery(row pgx.Row) (*domain.WebhookDelivery, error) {
	var d domain.WebhookDelivery
	var respCode *int
	var latencyMS *int64
	var errMsg *string
	err := row.Scan(
		&d.ID, &d.SubscriptionID, &d.EventType, &d.Payload,
		&d.Status, &d.Attempts,
		&respCode, &latencyMS, &errMsg,
		&d.CreatedAt, &d.LastAttemptedAt, &d.NextRetryAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scan delivery: %w", err)
	}
	if respCode != nil {
		d.ResponseCode = *respCode
	}
	if latencyMS != nil {
		d.LatencyMS = *latencyMS
	}
	if errMsg != nil {
		d.ErrorMessage = *errMsg
	}
	return &d, nil
}

func scanDeliveryRow(rows pgx.Rows) (*domain.WebhookDelivery, error) {
	var d domain.WebhookDelivery
	var respCode *int
	var latencyMS *int64
	var errMsg *string
	err := rows.Scan(
		&d.ID, &d.SubscriptionID, &d.EventType, &d.Payload,
		&d.Status, &d.Attempts,
		&respCode, &latencyMS, &errMsg,
		&d.CreatedAt, &d.LastAttemptedAt, &d.NextRetryAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan delivery row: %w", err)
	}
	if respCode != nil {
		d.ResponseCode = *respCode
	}
	if latencyMS != nil {
		d.LatencyMS = *latencyMS
	}
	if errMsg != nil {
		d.ErrorMessage = *errMsg
	}
	return &d, nil
}

func (r *WebhookRepo) CreateDelivery(ctx context.Context, d *domain.WebhookDelivery) error {
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	d.CreatedAt = time.Now()

	_, err := r.db.Exec(ctx, `
		INSERT INTO webhook_deliveries
		    (id, subscription_id, event_type, payload, status, attempts,
		     response_code, latency_ms, error_message, created_at, last_attempted_at, next_retry_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		d.ID, d.SubscriptionID, d.EventType, d.Payload,
		string(d.Status), d.Attempts,
		nilIfZeroInt(d.ResponseCode), nilIfZeroInt64(d.LatencyMS), nilIfEmpty(d.ErrorMessage),
		d.CreatedAt, d.LastAttemptedAt, d.NextRetryAt,
	)
	return err
}

func (r *WebhookRepo) GetDelivery(ctx context.Context, deliveryID uuid.UUID) (*domain.WebhookDelivery, error) {
	row := r.db.QueryRow(ctx,
		`SELECT `+deliveryCols+` FROM webhook_deliveries WHERE id = $1`,
		deliveryID,
	)
	return scanDelivery(row)
}

func (r *WebhookRepo) UpdateDelivery(ctx context.Context, d *domain.WebhookDelivery) error {
	_, err := r.db.Exec(ctx, `
		UPDATE webhook_deliveries
		SET status = $1, attempts = $2, response_code = $3, latency_ms = $4,
		    error_message = $5, last_attempted_at = $6, next_retry_at = $7
		WHERE id = $8`,
		string(d.Status), d.Attempts,
		nilIfZeroInt(d.ResponseCode), nilIfZeroInt64(d.LatencyMS),
		nilIfEmpty(d.ErrorMessage), d.LastAttemptedAt, d.NextRetryAt,
		d.ID,
	)
	return err
}

func (r *WebhookRepo) ListPendingDeliveries(ctx context.Context, limit int) ([]*domain.WebhookDelivery, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+deliveryCols+`
		FROM webhook_deliveries
		WHERE status IN ('pending', 'failed')
		  AND (next_retry_at IS NULL OR next_retry_at <= NOW())
		ORDER BY next_retry_at ASC NULLS FIRST
		LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list pending deliveries: %w", err)
	}
	defer rows.Close()

	var result []*domain.WebhookDelivery
	for rows.Next() {
		d, err := scanDeliveryRow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func nilIfZeroInt(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

func nilIfZeroInt64(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}
