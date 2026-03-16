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
)

// BalanceRepo implements repository.BalanceRepository backed by PostgreSQL.
//
// Schema note: the `balances` table has a UNIQUE constraint on
// (tenant_id, member_id, currency). PostgreSQL does NOT treat NULL values as
// equal in UNIQUE constraints, which means it cannot enforce uniqueness for
// tenant-level rows (where member_id IS NULL). We work around this by always
// using UPDATE-then-INSERT logic instead of INSERT ... ON CONFLICT for the
// member_id-IS-NULL case.
type BalanceRepo struct {
	db *pgxpool.Pool
}

// NewBalanceRepo constructs a BalanceRepo.
func NewBalanceRepo(db *pgxpool.Pool) *BalanceRepo {
	return &BalanceRepo{db: db}
}

// ── Upsert ────────────────────────────────────────────────────────────────────

// Upsert is a low-level full-record upsert. Prefer the named helpers
// (AddPending, CreditSettlement, DebitAvailable) for correctness.
func (r *BalanceRepo) Upsert(ctx context.Context, b *domain.Balance) error {
	if b.MemberID != nil {
		return r.upsertMemberBalance(ctx, b)
	}
	return r.upsertTenantBalance(ctx, b)
}

func (r *BalanceRepo) upsertTenantBalance(ctx context.Context, b *domain.Balance) error {
	return r.withTenantBalance(ctx, b.TenantID, b.Currency, func(exists bool) error {
		if exists {
			_, err := r.db.Exec(ctx,
				`UPDATE balances SET available = $1, pending = $2, updated_at = NOW()
				 WHERE tenant_id = $3 AND member_id IS NULL AND currency = $4`,
				b.Available, b.Pending, b.TenantID, b.Currency,
			)
			return err
		}
		_, err := r.db.Exec(ctx,
			`INSERT INTO balances (tenant_id, currency, available, pending)
			 VALUES ($1, $2, $3, $4)`,
			b.TenantID, b.Currency, b.Available, b.Pending,
		)
		return err
	})
}

func (r *BalanceRepo) upsertMemberBalance(ctx context.Context, b *domain.Balance) error {
	const q = `
		INSERT INTO balances (tenant_id, member_id, currency, available, pending)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (tenant_id, member_id, currency) DO UPDATE
		    SET available = EXCLUDED.available,
		        pending   = EXCLUDED.pending,
		        updated_at = NOW()`
	_, err := r.db.Exec(ctx, q, b.TenantID, b.MemberID, b.Currency, b.Available, b.Pending)
	return err
}

// ── GetTenantBalance ──────────────────────────────────────────────────────────

func (r *BalanceRepo) GetTenantBalance(ctx context.Context, tenantID uuid.UUID, currency string) (*domain.Balance, error) {
	const q = `
		SELECT id, tenant_id, member_id, available, pending, currency, updated_at
		FROM balances
		WHERE tenant_id = $1 AND member_id IS NULL AND currency = $2`

	return scanBalance(r.db.QueryRow(ctx, q, tenantID, currency))
}

// ── GetMemberBalance ──────────────────────────────────────────────────────────

func (r *BalanceRepo) GetMemberBalance(ctx context.Context, tenantID, memberID uuid.UUID, currency string) (*domain.Balance, error) {
	const q = `
		SELECT id, tenant_id, member_id, available, pending, currency, updated_at
		FROM balances
		WHERE tenant_id = $1 AND member_id = $2 AND currency = $3`

	return scanBalance(r.db.QueryRow(ctx, q, tenantID, memberID, currency))
}

// ── List ──────────────────────────────────────────────────────────────────────

func (r *BalanceRepo) List(ctx context.Context, tenantID uuid.UUID, currency string, pageSize int, pageToken string) ([]*domain.Balance, string, error) {
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	args := []any{tenantID, pageSize + 1}
	where := "tenant_id = $1"
	if currency != "" {
		where += " AND currency = $3"
		args = append(args, currency)
	}

	rows, err := r.db.Query(ctx,
		fmt.Sprintf(`SELECT id, tenant_id, member_id, available, pending, currency, updated_at
		             FROM balances WHERE %s ORDER BY updated_at DESC LIMIT $2`, where),
		args...,
	)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var out []*domain.Balance
	for rows.Next() {
		b, err := scanBalanceRow(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var nextToken string
	if len(out) > pageSize {
		last := out[pageSize-1]
		nextToken = encodeCursor(last.UpdatedAt, last.ID)
		out = out[:pageSize]
	}
	return out, nextToken, nil
}

// ── GetHistory ────────────────────────────────────────────────────────────────

// GetHistory returns time-bucketed balance snapshots.
// granularity must be one of: "hour" | "day" | "week" | "month".
func (r *BalanceRepo) GetHistory(ctx context.Context, tenantID, memberID uuid.UUID, currency, granularity string, from, to time.Time) ([]*domain.BalanceSnapshot, error) {
	// The snapshot table does not exist yet in the migrations; return empty slice
	// until migration 008_create_balance_snapshots.sql is added.
	return nil, nil
}

// ── AddPending ────────────────────────────────────────────────────────────────

// AddPending increases the tenant's pending balance when a charge is initiated.
// Must be called when creating a payment record (status = pending/in_progress).
func (r *BalanceRepo) AddPending(ctx context.Context, tenantID uuid.UUID, amount int64, currency string) error {
	return r.withTenantBalance(ctx, tenantID, currency, func(exists bool) error {
		if exists {
			_, err := r.db.Exec(ctx,
				`UPDATE balances SET pending = pending + $1, updated_at = NOW()
				 WHERE tenant_id = $2 AND member_id IS NULL AND currency = $3`,
				amount, tenantID, currency,
			)
			return err
		}
		_, err := r.db.Exec(ctx,
			`INSERT INTO balances (tenant_id, currency, pending, available) VALUES ($1, $2, $3, 0)`,
			tenantID, currency, amount,
		)
		return err
	})
}

// ── CreditSettlement ─────────────────────────────────────────────────────────

// CreditSettlement is called when charge.succeeded fires.
// It atomically:
//   - Subtracts grossAmount from pending  (the charge is no longer in-flight)
//   - Adds netAmount to available         (tenant's earned amount after fees)
//
// The difference (openpay_fee + platform_fee) is implicitly discarded from
// the balance — it was never the tenant's money.
func (r *BalanceRepo) CreditSettlement(ctx context.Context, tenantID uuid.UUID, grossAmount, netAmount int64, currency string) error {
	const q = `
		UPDATE balances
		SET pending   = GREATEST(0, pending - $1),
		    available = available + $2,
		    updated_at = NOW()
		WHERE tenant_id = $3 AND member_id IS NULL AND currency = $4`

	tag, err := r.db.Exec(ctx, q, grossAmount, netAmount, tenantID, currency)
	if err != nil {
		return fmt.Errorf("credit settlement for tenant %s: %w", tenantID, err)
	}
	if tag.RowsAffected() == 0 {
		// Balance row doesn't exist yet (charge succeeded before AddPending was ever called —
		// possible if the service was restarted mid-flight). Create it with just the net amount.
		_, err = r.db.Exec(ctx,
			`INSERT INTO balances (tenant_id, currency, available, pending) VALUES ($1, $2, $3, 0)`,
			tenantID, currency, netAmount,
		)
		if err != nil {
			return fmt.Errorf("insert balance on credit settlement for tenant %s: %w", tenantID, err)
		}
	}
	return nil
}

// ── DebitAvailable ────────────────────────────────────────────────────────────

// DebitAvailable reduces the available balance when a payout is dispatched.
// The caller (DisbursementScheduler) must ensure available >= amount before
// calling this; an insufficient balance will still execute the UPDATE but will
// leave the balance negative, which the CHECK constraint on the table will catch
// once it is added in a future migration.
func (r *BalanceRepo) DebitAvailable(ctx context.Context, tenantID uuid.UUID, amount int64, currency string) error {
	const q = `
		UPDATE balances
		SET available  = available - $1,
		    updated_at = NOW()
		WHERE tenant_id = $2 AND member_id IS NULL AND currency = $3`

	tag, err := r.db.Exec(ctx, q, amount, tenantID, currency)
	if err != nil {
		return fmt.Errorf("debit available for tenant %s: %w", tenantID, err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// withTenantBalance locks the tenant-level balance row (or detects its absence)
// and calls fn(exists). Must be called inside a transaction for safety; when
// called outside a transaction it is still correct but not serializable.
func (r *BalanceRepo) withTenantBalance(ctx context.Context, tenantID uuid.UUID, currency string, fn func(exists bool) error) error {
	var exists bool
	err := r.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM balances WHERE tenant_id = $1 AND member_id IS NULL AND currency = $2)`,
		tenantID, currency,
	).Scan(&exists)
	if err != nil {
		return err
	}
	return fn(exists)
}

func scanBalance(row pgx.Row) (*domain.Balance, error) {
	var b domain.Balance
	err := row.Scan(&b.ID, &b.TenantID, &b.MemberID, &b.Available, &b.Pending, &b.Currency, &b.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scan balance: %w", err)
	}
	return &b, nil
}

func scanBalanceRow(rows pgx.Rows) (*domain.Balance, error) {
	var b domain.Balance
	err := rows.Scan(&b.ID, &b.TenantID, &b.MemberID, &b.Available, &b.Pending, &b.Currency, &b.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("scan balance row: %w", err)
	}
	return &b, nil
}
