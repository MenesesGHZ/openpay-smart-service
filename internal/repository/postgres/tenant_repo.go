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
	"github.com/menesesghz/openpay-smart-service/internal/encrypt"
)

// TenantRepo implements repository.TenantRepository backed by PostgreSQL.
type TenantRepo struct {
	db     *pgxpool.Pool
	aesKey string // hex-encoded 32-byte AES key for CLABE encryption
}

// NewTenantRepo constructs a TenantRepo.
// aesKey must be a 64-character hex string (32 bytes); generated with `make gen-aes-key`.
func NewTenantRepo(db *pgxpool.Pool, aesKey string) *TenantRepo {
	return &TenantRepo{db: db, aesKey: aesKey}
}

// ── Create ────────────────────────────────────────────────────────────────────

func (r *TenantRepo) Create(ctx context.Context, t *domain.Tenant) error {
	const q = `
		INSERT INTO tenants (id, name, api_key_hash, tier, platform_fee_bps, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`

	_, err := r.db.Exec(ctx, q,
		t.ID, t.Name, t.APIKeyHash, t.Tier, t.PlatformFeeBPS,
		t.CreatedAt, t.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("tenant create: %w", err)
	}
	return nil
}

// ── GetByID ───────────────────────────────────────────────────────────────────

func (r *TenantRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Tenant, error) {
	const q = `
		SELECT id, name, api_key_hash, tier, platform_fee_bps,
		       bank_clabe_enc, bank_clabe_mask, bank_holder_name, bank_name,
		       created_at, updated_at
		FROM tenants
		WHERE id = $1`

	row := r.db.QueryRow(ctx, q, id)
	return r.scanTenant(row)
}

// ── GetByAPIKeyHash ───────────────────────────────────────────────────────────

func (r *TenantRepo) GetByAPIKeyHash(ctx context.Context, hash string) (*domain.Tenant, error) {
	const q = `
		SELECT id, name, api_key_hash, tier, platform_fee_bps,
		       bank_clabe_enc, bank_clabe_mask, bank_holder_name, bank_name,
		       created_at, updated_at
		FROM tenants
		WHERE api_key_hash = $1`

	row := r.db.QueryRow(ctx, q, hash)
	return r.scanTenant(row)
}

// ── Update ────────────────────────────────────────────────────────────────────

func (r *TenantRepo) Update(ctx context.Context, t *domain.Tenant) error {
	const q = `
		UPDATE tenants
		SET name = $1, tier = $2, platform_fee_bps = $3, updated_at = NOW()
		WHERE id = $4`

	tag, err := r.db.Exec(ctx, q, t.Name, t.Tier, t.PlatformFeeBPS, t.ID)
	if err != nil {
		return fmt.Errorf("tenant update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ── SetBankAccount ────────────────────────────────────────────────────────────

func (r *TenantRepo) SetBankAccount(ctx context.Context, tenantID uuid.UUID, clabe, holderName, bankName string) error {
	enc, err := encrypt.Encrypt(r.aesKey, clabe)
	if err != nil {
		return fmt.Errorf("encrypt CLABE: %w", err)
	}
	mask := encrypt.MaskCLABE(clabe)

	const q = `
		UPDATE tenants
		SET bank_clabe_enc = $1, bank_clabe_mask = $2,
		    bank_holder_name = $3, bank_name = $4, updated_at = NOW()
		WHERE id = $5`

	tag, err := r.db.Exec(ctx, q, enc, mask, holderName, bankName, tenantID)
	if err != nil {
		return fmt.Errorf("set bank account: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ── ClearBankAccount ──────────────────────────────────────────────────────────

func (r *TenantRepo) ClearBankAccount(ctx context.Context, tenantID uuid.UUID) error {
	const q = `
		UPDATE tenants
		SET bank_clabe_enc = NULL, bank_clabe_mask = NULL,
		    bank_holder_name = NULL, bank_name = NULL, updated_at = NOW()
		WHERE id = $1`

	tag, err := r.db.Exec(ctx, q, tenantID)
	if err != nil {
		return fmt.Errorf("clear bank account: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ── UpsertSchedule ────────────────────────────────────────────────────────────

func (r *TenantRepo) UpsertSchedule(ctx context.Context, s *domain.DisbursementSchedule) error {
	const q = `
		INSERT INTO disbursement_schedules
		    (tenant_id, frequency, cron_expr, enabled, next_run_at, last_run_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
		ON CONFLICT (tenant_id) DO UPDATE
		    SET frequency   = EXCLUDED.frequency,
		        cron_expr   = EXCLUDED.cron_expr,
		        enabled     = EXCLUDED.enabled,
		        next_run_at = EXCLUDED.next_run_at,
		        last_run_at = EXCLUDED.last_run_at,
		        updated_at  = NOW()`

	var lastRun *time.Time
	if !s.LastRunAt.IsZero() {
		lastRun = &s.LastRunAt
	}
	var nextRun *time.Time
	if !s.NextRunAt.IsZero() {
		nextRun = &s.NextRunAt
	}

	_, err := r.db.Exec(ctx, q,
		s.TenantID, s.Frequency, nilIfEmpty(s.CronExpr),
		s.Enabled, nextRun, lastRun,
	)
	return err
}

// ── GetSchedule ───────────────────────────────────────────────────────────────

func (r *TenantRepo) GetSchedule(ctx context.Context, tenantID uuid.UUID) (*domain.DisbursementSchedule, error) {
	const q = `
		SELECT tenant_id, frequency, cron_expr, enabled, next_run_at, last_run_at, updated_at
		FROM disbursement_schedules
		WHERE tenant_id = $1`

	var s domain.DisbursementSchedule
	var cronExpr *string
	var nextRun, lastRun *time.Time

	err := r.db.QueryRow(ctx, q, tenantID).Scan(
		&s.TenantID, &s.Frequency, &cronExpr, &s.Enabled,
		&nextRun, &lastRun, &s.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get schedule: %w", err)
	}
	if cronExpr != nil {
		s.CronExpr = *cronExpr
	}
	if nextRun != nil {
		s.NextRunAt = *nextRun
	}
	if lastRun != nil {
		s.LastRunAt = *lastRun
	}
	return &s, nil
}

// ── ListDueSchedules ─────────────────────────────────────────────────────────

func (r *TenantRepo) ListDueSchedules(ctx context.Context, before time.Time) ([]*domain.DisbursementSchedule, error) {
	const q = `
		SELECT tenant_id, frequency, cron_expr, enabled, next_run_at, last_run_at, updated_at
		FROM disbursement_schedules
		WHERE enabled = TRUE AND next_run_at <= $1
		ORDER BY next_run_at ASC`

	rows, err := r.db.Query(ctx, q, before)
	if err != nil {
		return nil, fmt.Errorf("list due schedules: %w", err)
	}
	defer rows.Close()

	var out []*domain.DisbursementSchedule
	for rows.Next() {
		var s domain.DisbursementSchedule
		var cronExpr *string
		var nextRun, lastRun *time.Time

		if err := rows.Scan(
			&s.TenantID, &s.Frequency, &cronExpr, &s.Enabled,
			&nextRun, &lastRun, &s.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan schedule: %w", err)
		}
		if cronExpr != nil {
			s.CronExpr = *cronExpr
		}
		if nextRun != nil {
			s.NextRunAt = *nextRun
		}
		if lastRun != nil {
			s.LastRunAt = *lastRun
		}
		out = append(out, &s)
	}
	return out, rows.Err()
}

// ── scanTenant ────────────────────────────────────────────────────────────────

// scanTenant reads a single tenant row. The bank_clabe_enc column is decrypted
// in-memory; the caller only ever sees the plaintext CLABE. The masked version
// is also returned for use in API responses that should not expose the full CLABE.
func (r *TenantRepo) scanTenant(row pgx.Row) (*domain.Tenant, error) {
	var t domain.Tenant
	var clabeEnc, clabeMask, holderName, bankName *string

	err := row.Scan(
		&t.ID, &t.Name, &t.APIKeyHash, &t.Tier, &t.PlatformFeeBPS,
		&clabeEnc, &clabeMask, &holderName, &bankName,
		&t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scan tenant: %w", err)
	}

	if clabeEnc != nil && *clabeEnc != "" {
		plain, err := encrypt.Decrypt(r.aesKey, *clabeEnc)
		if err != nil {
			return nil, fmt.Errorf("decrypt CLABE for tenant %s: %w", t.ID, err)
		}
		t.BankCLABE = plain
	}
	if holderName != nil {
		t.BankHolderName = *holderName
	}
	if bankName != nil {
		t.BankName = *bankName
	}

	return &t, nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
