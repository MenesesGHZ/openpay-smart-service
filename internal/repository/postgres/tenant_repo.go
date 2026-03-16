package postgres

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/menesesghz/openpay-smart-service/internal/domain"
	"github.com/menesesghz/openpay-smart-service/internal/encrypt"
	"github.com/menesesghz/openpay-smart-service/internal/repository"
)

const defaultTenantPageSize = 20

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
		INSERT INTO tenants (id, name, api_key_hash, api_key_prefix, tier, platform_fee_bps, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	_, err := r.db.Exec(ctx, q,
		t.ID, t.Name, t.APIKeyHash, t.APIKeyPrefix, t.Tier, t.PlatformFeeBPS,
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
		SELECT id, name, api_key_hash, api_key_prefix, tier, platform_fee_bps,
		       bank_clabe_enc, bank_clabe_mask, bank_holder_name, bank_name,
		       created_at, updated_at, deleted_at
		FROM tenants
		WHERE id = $1 AND deleted_at IS NULL`

	row := r.db.QueryRow(ctx, q, id)
	return r.scanTenant(row)
}

// ── GetByAPIKeyHash ───────────────────────────────────────────────────────────

func (r *TenantRepo) GetByAPIKeyHash(ctx context.Context, hash string) (*domain.Tenant, error) {
	const q = `
		SELECT id, name, api_key_hash, api_key_prefix, tier, platform_fee_bps,
		       bank_clabe_enc, bank_clabe_mask, bank_holder_name, bank_name,
		       created_at, updated_at, deleted_at
		FROM tenants
		WHERE api_key_hash = $1 AND deleted_at IS NULL`

	row := r.db.QueryRow(ctx, q, hash)
	return r.scanTenant(row)
}

// ── Update ────────────────────────────────────────────────────────────────────

func (r *TenantRepo) Update(ctx context.Context, t *domain.Tenant) error {
	const q = `
		UPDATE tenants
		SET name = $1, tier = $2, platform_fee_bps = $3, updated_at = NOW()
		WHERE id = $4 AND deleted_at IS NULL`

	tag, err := r.db.Exec(ctx, q, t.Name, t.Tier, t.PlatformFeeBPS, t.ID)
	if err != nil {
		return fmt.Errorf("tenant update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ── List ──────────────────────────────────────────────────────────────────────

// List returns active tenants ordered by created_at ASC, id ASC.
// Pagination is offset-based; the page_token encodes the current offset.
func (r *TenantRepo) List(ctx context.Context, opts repository.ListTenantsOptions) ([]*domain.Tenant, string, error) {
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = defaultTenantPageSize
	}

	offset := decodePageToken(opts.PageToken)

	const q = `
		SELECT id, name, api_key_hash, api_key_prefix, tier, platform_fee_bps,
		       bank_clabe_enc, bank_clabe_mask, bank_holder_name, bank_name,
		       created_at, updated_at, deleted_at
		FROM tenants
		WHERE deleted_at IS NULL
		  AND ($1 = '' OR tier = $1)
		ORDER BY created_at ASC, id ASC
		LIMIT $2 OFFSET $3`

	rows, err := r.db.Query(ctx, q, opts.Tier, pageSize+1, offset)
	if err != nil {
		return nil, "", fmt.Errorf("list tenants: %w", err)
	}
	defer rows.Close()

	var tenants []*domain.Tenant
	for rows.Next() {
		t, err := r.scanTenantRow(rows)
		if err != nil {
			return nil, "", err
		}
		tenants = append(tenants, t)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("list tenants scan: %w", err)
	}

	var nextToken string
	if len(tenants) > pageSize {
		tenants = tenants[:pageSize]
		nextToken = encodePageToken(offset + pageSize)
	}

	return tenants, nextToken, nil
}

// ── Delete ────────────────────────────────────────────────────────────────────

// Delete soft-deletes a tenant. The tenant's API key immediately stops working.
func (r *TenantRepo) Delete(ctx context.Context, id uuid.UUID) error {
	const q = `
		UPDATE tenants
		SET deleted_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL`

	tag, err := r.db.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("tenant delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ── RotateAPIKey ──────────────────────────────────────────────────────────────

// RotateAPIKey atomically replaces the tenant's api_key_hash and api_key_prefix.
// The previous key stops working immediately.
func (r *TenantRepo) RotateAPIKey(ctx context.Context, id uuid.UUID, newHash, newPrefix string) error {
	const q = `
		UPDATE tenants
		SET api_key_hash = $1, api_key_prefix = $2, updated_at = NOW()
		WHERE id = $3 AND deleted_at IS NULL`

	tag, err := r.db.Exec(ctx, q, newHash, newPrefix, id)
	if err != nil {
		return fmt.Errorf("rotate api key: %w", err)
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
		WHERE id = $5 AND deleted_at IS NULL`

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
		WHERE id = $1 AND deleted_at IS NULL`

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

// scanTenant reads a single tenant row from a pgx.Row (QueryRow result).
func (r *TenantRepo) scanTenant(row pgx.Row) (*domain.Tenant, error) {
	var t domain.Tenant
	var clabeEnc, clabeMask, holderName, bankName *string

	err := row.Scan(
		&t.ID, &t.Name, &t.APIKeyHash, &t.APIKeyPrefix, &t.Tier, &t.PlatformFeeBPS,
		&clabeEnc, &clabeMask, &holderName, &bankName,
		&t.CreatedAt, &t.UpdatedAt, &t.DeletedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scan tenant: %w", err)
	}

	return r.hydrateTenant(&t, clabeEnc, holderName, bankName)
}

// scanTenantRow reads a single tenant row from pgx.Rows (Query result).
func (r *TenantRepo) scanTenantRow(rows pgx.Rows) (*domain.Tenant, error) {
	var t domain.Tenant
	var clabeEnc, clabeMask, holderName, bankName *string

	if err := rows.Scan(
		&t.ID, &t.Name, &t.APIKeyHash, &t.APIKeyPrefix, &t.Tier, &t.PlatformFeeBPS,
		&clabeEnc, &clabeMask, &holderName, &bankName,
		&t.CreatedAt, &t.UpdatedAt, &t.DeletedAt,
	); err != nil {
		return nil, fmt.Errorf("scan tenant row: %w", err)
	}

	return r.hydrateTenant(&t, clabeEnc, holderName, bankName)
}

// hydrateTenant decrypts the CLABE and fills nullable string fields.
func (r *TenantRepo) hydrateTenant(t *domain.Tenant, clabeEnc, holderName, bankName *string) (*domain.Tenant, error) {
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
	return t, nil
}

// ── Pagination helpers ────────────────────────────────────────────────────────

func encodePageToken(offset int) string {
	return base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func decodePageToken(token string) int {
	if token == "" {
		return 0
	}
	b, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(string(b))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
