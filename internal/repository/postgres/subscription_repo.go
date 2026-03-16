package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/menesesghz/openpay-smart-service/internal/domain"
	"github.com/menesesghz/openpay-smart-service/internal/repository"
)

// ─── PlanRepo ────────────────────────────────────────────────────────────────

type PlanRepo struct{ db *pgxpool.Pool }

func NewPlanRepo(db *pgxpool.Pool) *PlanRepo { return &PlanRepo{db: db} }

const planCols = `id, tenant_id, openpay_plan_id, name, amount, currency,
	repeat_every, repeat_unit, trial_days, retry_times, status_on_retry_end,
	active, created_at, updated_at`

func (r *PlanRepo) Create(ctx context.Context, p *domain.Plan) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	now := time.Now()
	p.CreatedAt = now
	p.UpdatedAt = now

	_, err := r.db.Exec(ctx, `
		INSERT INTO plans (id, tenant_id, openpay_plan_id, name, amount, currency,
			repeat_every, repeat_unit, trial_days, retry_times, status_on_retry_end,
			active, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		p.ID, p.TenantID, p.OpenpayPlanID, p.Name, p.Amount, p.Currency,
		p.RepeatEvery, p.RepeatUnit, p.TrialDays, p.RetryTimes, p.StatusOnRetryEnd,
		p.Active, p.CreatedAt, p.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("plans.Create: %w", err)
	}
	return nil
}

func (r *PlanRepo) GetByID(ctx context.Context, tenantID, planID uuid.UUID) (*domain.Plan, error) {
	row := r.db.QueryRow(ctx,
		`SELECT `+planCols+` FROM plans WHERE id=$1 AND tenant_id=$2`,
		planID, tenantID,
	)
	p, err := scanPlan(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("plans.GetByID: %w", err)
	}
	return p, nil
}

func (r *PlanRepo) GetByOpenpayPlanID(ctx context.Context, openpayPlanID string) (*domain.Plan, error) {
	row := r.db.QueryRow(ctx,
		`SELECT `+planCols+` FROM plans WHERE openpay_plan_id=$1`,
		openpayPlanID,
	)
	p, err := scanPlan(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("plans.GetByOpenpayPlanID: %w", err)
	}
	return p, nil
}

func (r *PlanRepo) List(ctx context.Context, opts repository.ListPlansOptions) ([]*domain.Plan, string, error) {
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}

	args := []any{opts.TenantID}
	where := "tenant_id = $1"
	if opts.ActiveOnly {
		where += " AND active = TRUE"
	}

	// Cursor: base64(created_at|id)
	if opts.PageToken != "" {
		ts, id, err := decodeCursor(opts.PageToken)
		if err == nil {
			args = append(args, ts, id)
			where += fmt.Sprintf(" AND (created_at, id) < ($%d, $%d)", len(args)-1, len(args))
		}
	}

	args = append(args, pageSize+1)
	query := fmt.Sprintf(
		`SELECT `+planCols+` FROM plans WHERE %s ORDER BY created_at DESC, id DESC LIMIT $%d`,
		where, len(args),
	)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("plans.List: %w", err)
	}
	defer rows.Close()

	var plans []*domain.Plan
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, "", err
		}
		plans = append(plans, p)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("plans.List rows: %w", err)
	}

	var nextToken string
	if len(plans) > pageSize {
		last := plans[pageSize-1]
		nextToken = encodeCursor(last.CreatedAt, last.ID)
		plans = plans[:pageSize]
	}
	return plans, nextToken, nil
}

func (r *PlanRepo) Deactivate(ctx context.Context, tenantID, planID uuid.UUID) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE plans SET active=FALSE, updated_at=NOW() WHERE id=$1 AND tenant_id=$2`,
		planID, tenantID,
	)
	if err != nil {
		return fmt.Errorf("plans.Deactivate: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func scanPlan(row pgx.Row) (*domain.Plan, error) {
	var p domain.Plan
	err := row.Scan(
		&p.ID, &p.TenantID, &p.OpenpayPlanID, &p.Name, &p.Amount, &p.Currency,
		&p.RepeatEvery, &p.RepeatUnit, &p.TrialDays, &p.RetryTimes, &p.StatusOnRetryEnd,
		&p.Active, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// ─── SubscriptionRepo ────────────────────────────────────────────────────────

type SubscriptionRepo struct{ db *pgxpool.Pool }

func NewSubscriptionRepo(db *pgxpool.Pool) *SubscriptionRepo { return &SubscriptionRepo{db: db} }

const subCols = `id, tenant_id, member_id, plan_id, openpay_sub_id, source_card_id,
	status, trial_end_date, period_end_date, cancel_at_period_end,
	failed_charge_count, last_charge_id, created_at, updated_at`

func (r *SubscriptionRepo) Create(ctx context.Context, s *domain.Subscription) error {
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	now := time.Now()
	s.CreatedAt = now
	s.UpdatedAt = now

	_, err := r.db.Exec(ctx, `
		INSERT INTO subscriptions (id, tenant_id, member_id, plan_id, openpay_sub_id,
			source_card_id, status, trial_end_date, period_end_date, cancel_at_period_end,
			failed_charge_count, last_charge_id, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		s.ID, s.TenantID, s.MemberID, s.PlanID, s.OpenpaySubID,
		s.SourceCardID, string(s.Status), s.TrialEndDate, s.PeriodEndDate,
		s.CancelAtPeriodEnd, s.FailedChargeCount, s.LastChargeID,
		s.CreatedAt, s.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("subscriptions.Create: %w", err)
	}
	return nil
}

func (r *SubscriptionRepo) GetByID(ctx context.Context, tenantID, subID uuid.UUID) (*domain.Subscription, error) {
	row := r.db.QueryRow(ctx,
		`SELECT `+subCols+` FROM subscriptions WHERE id=$1 AND tenant_id=$2`,
		subID, tenantID,
	)
	s, err := scanSub(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("subscriptions.GetByID: %w", err)
	}
	return s, nil
}

func (r *SubscriptionRepo) GetByOpenpaySubID(ctx context.Context, openpaySubID string) (*domain.Subscription, error) {
	row := r.db.QueryRow(ctx,
		`SELECT `+subCols+` FROM subscriptions WHERE openpay_sub_id=$1`,
		openpaySubID,
	)
	s, err := scanSub(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("subscriptions.GetByOpenpaySubID: %w", err)
	}
	return s, nil
}

func (r *SubscriptionRepo) List(ctx context.Context, opts repository.ListSubscriptionsOptions) ([]*domain.Subscription, string, error) {
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}

	args := []any{opts.TenantID}
	where := "tenant_id = $1"

	if opts.MemberID != nil {
		args = append(args, *opts.MemberID)
		where += fmt.Sprintf(" AND member_id = $%d", len(args))
	}
	if opts.PlanID != nil {
		args = append(args, *opts.PlanID)
		where += fmt.Sprintf(" AND plan_id = $%d", len(args))
	}
	if len(opts.Statuses) > 0 {
		args = append(args, opts.Statuses)
		where += fmt.Sprintf(" AND status = ANY($%d)", len(args))
	}

	if opts.PageToken != "" {
		ts, id, err := decodeCursor(opts.PageToken)
		if err == nil {
			args = append(args, ts, id)
			where += fmt.Sprintf(" AND (created_at, id) < ($%d, $%d)", len(args)-1, len(args))
		}
	}

	args = append(args, pageSize+1)
	query := fmt.Sprintf(
		`SELECT `+subCols+` FROM subscriptions WHERE %s ORDER BY created_at DESC, id DESC LIMIT $%d`,
		where, len(args),
	)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("subscriptions.List: %w", err)
	}
	defer rows.Close()

	var subs []*domain.Subscription
	for rows.Next() {
		s, err := scanSub(rows)
		if err != nil {
			return nil, "", err
		}
		subs = append(subs, s)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("subscriptions.List rows: %w", err)
	}

	var nextToken string
	if len(subs) > pageSize {
		last := subs[pageSize-1]
		nextToken = encodeCursor(last.CreatedAt, last.ID)
		subs = subs[:pageSize]
	}
	return subs, nextToken, nil
}

func (r *SubscriptionRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.SubscriptionStatus) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE subscriptions SET status=$1, updated_at=NOW() WHERE id=$2`,
		string(status), id,
	)
	if err != nil {
		return fmt.Errorf("subscriptions.UpdateStatus: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// RecordCharge is called on subscription.charge.succeeded.
// It sets last_charge_id, resets failed_charge_count, and transitions the
// subscription to "active" (it may have been "trial" or "past_due").
func (r *SubscriptionRepo) RecordCharge(ctx context.Context, id uuid.UUID, chargePaymentID uuid.UUID) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE subscriptions
		   SET last_charge_id      = $1,
		       failed_charge_count = 0,
		       status              = CASE
		                               WHEN status IN ('trial','past_due') THEN 'active'
		                               ELSE status
		                             END,
		       updated_at          = NOW()
		 WHERE id = $2`,
		chargePaymentID, id,
	)
	if err != nil {
		return fmt.Errorf("subscriptions.RecordCharge: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// IncrementFailedCharge bumps the failure counter and marks the subscription
// as "past_due".  The plan-level retry logic (status_on_retry_end) is
// enforced by OpenPay itself; we mirror that outcome when the
// subscription.cancelled or subscription.deactivated webhook arrives.
func (r *SubscriptionRepo) IncrementFailedCharge(ctx context.Context, id uuid.UUID) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE subscriptions
		   SET failed_charge_count = failed_charge_count + 1,
		       status              = 'past_due',
		       updated_at          = NOW()
		 WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("subscriptions.IncrementFailedCharge: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// SetCancelAtPeriodEnd flags the subscription to cancel at the end of the current period.
// The subscription remains "active" until period_end_date; a scheduled job should
// then call UpdateStatus(id, SubscriptionStatusCancelled).
func (r *SubscriptionRepo) SetCancelAtPeriodEnd(ctx context.Context, id uuid.UUID) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE subscriptions SET cancel_at_period_end=TRUE, updated_at=NOW() WHERE id=$1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("subscriptions.SetCancelAtPeriodEnd: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func scanSub(row pgx.Row) (*domain.Subscription, error) {
	var s domain.Subscription
	var status string
	err := row.Scan(
		&s.ID, &s.TenantID, &s.MemberID, &s.PlanID, &s.OpenpaySubID, &s.SourceCardID,
		&status, &s.TrialEndDate, &s.PeriodEndDate, &s.CancelAtPeriodEnd,
		&s.FailedChargeCount, &s.LastChargeID, &s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	s.Status = domain.SubscriptionStatus(status)
	return &s, nil
}
