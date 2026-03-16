package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/menesesghz/openpay-smart-service/internal/domain"
	"github.com/menesesghz/openpay-smart-service/internal/repository"
)

// PaymentRepo implements repository.PaymentRepository backed by PostgreSQL.
type PaymentRepo struct {
	db *pgxpool.Pool
}

// NewPaymentRepo constructs a PaymentRepo.
func NewPaymentRepo(db *pgxpool.Pool) *PaymentRepo {
	return &PaymentRepo{db: db}
}

// ── Create ────────────────────────────────────────────────────────────────────

func (r *PaymentRepo) Create(ctx context.Context, p *domain.Payment) error {
	const q = `
		INSERT INTO payments (
			id, tenant_id, member_id, link_id, subscription_id,
			openpay_transaction_id, order_id, idempotency_key,
			gross_amount, openpay_fee, platform_fee, net_amount,
			currency, method, status, description,
			error_message, error_code,
			bank_clabe, bank_name, bank_reference, bank_agreement,
			metadata, created_at, updated_at
		) VALUES (
			$1,  $2,  $3,  $4,  $5,
			$6,  $7,  $8,
			$9,  $10, $11, $12,
			$13, $14, $15, $16,
			$17, $18,
			$19, $20, $21, $22,
			$23, $24, $25
		)`

	_, err := r.db.Exec(ctx, q,
		p.ID, p.TenantID, p.MemberID, p.LinkID, p.SubscriptionID,
		p.OpenpayTransactionID, nilIfEmpty(p.OrderID), p.IdempotencyKey,
		p.GrossAmount, p.OpenpayFee, p.PlatformFee, p.NetAmount,
		p.Currency, string(p.Method), string(p.Status), nilIfEmpty(p.Description),
		nilIfEmpty(p.ErrorMessage), nilIfEmpty(p.ErrorCode),
		nilIfEmpty(p.BankCLABE), nilIfEmpty(p.BankName),
		nilIfEmpty(p.BankReference), nilIfEmpty(p.BankAgreement),
		p.Metadata, p.CreatedAt, p.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("payment create: %w", err)
	}
	return nil
}

// ── GetByID ───────────────────────────────────────────────────────────────────

func (r *PaymentRepo) GetByID(ctx context.Context, tenantID, paymentID uuid.UUID) (*domain.Payment, error) {
	q := `SELECT ` + paymentCols + ` FROM payments WHERE id = $1 AND tenant_id = $2`
	row := r.db.QueryRow(ctx, q, paymentID, tenantID)
	return scanPayment(row)
}

// ── GetByOpenpayTransactionID ─────────────────────────────────────────────────

func (r *PaymentRepo) GetByOpenpayTransactionID(ctx context.Context, txID string) (*domain.Payment, error) {
	q := `SELECT ` + paymentCols + ` FROM payments WHERE openpay_transaction_id = $1`
	row := r.db.QueryRow(ctx, q, txID)
	return scanPayment(row)
}

// ── GetByIdempotencyKey ───────────────────────────────────────────────────────

func (r *PaymentRepo) GetByIdempotencyKey(ctx context.Context, tenantID uuid.UUID, key string) (*domain.Payment, error) {
	q := `SELECT ` + paymentCols + ` FROM payments WHERE tenant_id = $1 AND idempotency_key = $2`
	row := r.db.QueryRow(ctx, q, tenantID, key)
	return scanPayment(row)
}

// ── UpdateStatus ──────────────────────────────────────────────────────────────

// UpdateStatus changes a payment's status and optionally records error details.
// openpayErr[0] = error message, openpayErr[1] = error code (both optional).
func (r *PaymentRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.PaymentStatus, openpayErr ...string) error {
	var errMsg, errCode *string
	if len(openpayErr) > 0 && openpayErr[0] != "" {
		errMsg = &openpayErr[0]
	}
	if len(openpayErr) > 1 && openpayErr[1] != "" {
		errCode = &openpayErr[1]
	}

	const q = `
		UPDATE payments
		SET status        = $1,
		    error_message = COALESCE($2, error_message),
		    error_code    = COALESCE($3, error_code),
		    updated_at    = NOW()
		WHERE id = $4`

	tag, err := r.db.Exec(ctx, q, string(status), errMsg, errCode, id)
	if err != nil {
		return fmt.Errorf("update payment status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ── SettlePayment ─────────────────────────────────────────────────────────────

// SettlePayment atomically writes the full fee breakdown and marks the payment
// as completed in a single UPDATE statement.
// This is the only correct path for completing a payment — never use UpdateStatus.
func (r *PaymentRepo) SettlePayment(ctx context.Context, id uuid.UUID, fees repository.PaymentFees) error {
	const q = `
		UPDATE payments
		SET gross_amount = $1,
		    openpay_fee  = $2,
		    platform_fee = $3,
		    net_amount   = $4,
		    status       = 'completed',
		    updated_at   = NOW()
		WHERE id = $5`

	tag, err := r.db.Exec(ctx, q,
		fees.GrossAmount, fees.OpenpayFee, fees.PlatformFee, fees.NetAmount, id,
	)
	if err != nil {
		return fmt.Errorf("settle payment %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ── List ──────────────────────────────────────────────────────────────────────

func (r *PaymentRepo) List(ctx context.Context, opts repository.ListPaymentsOptions) ([]*domain.Payment, string, error) {
	args := []any{opts.TenantID}
	where := "tenant_id = $1"
	idx := 2

	if opts.MemberID != nil {
		where += fmt.Sprintf(" AND member_id = $%d", idx)
		args = append(args, *opts.MemberID)
		idx++
	}
	if opts.SubscriptionID != nil {
		where += fmt.Sprintf(" AND subscription_id = $%d", idx)
		args = append(args, *opts.SubscriptionID)
		idx++
	}
	if len(opts.Statuses) > 0 {
		where += fmt.Sprintf(" AND status = ANY($%d)", idx)
		args = append(args, opts.Statuses)
		idx++
	}
	if len(opts.Methods) > 0 {
		where += fmt.Sprintf(" AND method = ANY($%d)", idx)
		args = append(args, opts.Methods)
		idx++
	}
	if opts.Currency != "" {
		where += fmt.Sprintf(" AND currency = $%d", idx)
		args = append(args, opts.Currency)
		idx++
	}
	if opts.AmountMin != nil {
		where += fmt.Sprintf(" AND gross_amount >= $%d", idx)
		args = append(args, *opts.AmountMin)
		idx++
	}
	if opts.AmountMax != nil {
		where += fmt.Sprintf(" AND gross_amount <= $%d", idx)
		args = append(args, *opts.AmountMax)
		idx++
	}
	if opts.OrderID != "" {
		where += fmt.Sprintf(" AND order_id = $%d", idx)
		args = append(args, opts.OrderID)
		idx++
	}
	if opts.From != nil {
		where += fmt.Sprintf(" AND created_at >= $%d", idx)
		args = append(args, *opts.From)
		idx++
	}
	if opts.To != nil {
		where += fmt.Sprintf(" AND created_at <= $%d", idx)
		args = append(args, *opts.To)
		idx++
	}
	if opts.PageToken != "" {
		cursorAt, cursorID, err := decodeCursor(opts.PageToken)
		if err == nil {
			where += fmt.Sprintf(" AND (created_at, id) < ($%d, $%d)", idx, idx+1)
			args = append(args, cursorAt, cursorID)
			idx += 2
		}
	}

	pageSize := opts.PageSize
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	args = append(args, pageSize+1) // fetch one extra to determine if there's a next page

	q := fmt.Sprintf(
		`SELECT `+paymentCols+` FROM payments WHERE %s ORDER BY created_at DESC, id DESC LIMIT $%d`,
		where, idx,
	)

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list payments: %w", err)
	}
	defer rows.Close()

	var out []*domain.Payment
	for rows.Next() {
		p, err := scanPaymentRow(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var nextToken string
	if len(out) > pageSize {
		last := out[pageSize-1]
		nextToken = encodeCursor(last.CreatedAt, last.ID)
		out = out[:pageSize]
	}

	return out, nextToken, nil
}

// ── Payout ────────────────────────────────────────────────────────────────────

func (r *PaymentRepo) CreatePayout(ctx context.Context, p *domain.Payout) error {
	const q = `
		INSERT INTO payouts
		    (id, tenant_id, openpay_transaction_id, amount, currency, status, description, scheduled_for, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`

	_, err := r.db.Exec(ctx, q,
		p.ID, p.TenantID, nilIfEmpty(p.OpenpayTransactionID),
		p.Amount, p.Currency, string(p.Status),
		nilIfEmpty(p.Description), p.ScheduledFor,
		p.CreatedAt, p.UpdatedAt,
	)
	return err
}

func (r *PaymentRepo) GetPayoutByID(ctx context.Context, tenantID, payoutID uuid.UUID) (*domain.Payout, error) {
	const q = `
		SELECT id, tenant_id, openpay_transaction_id, amount, currency,
		       status, description, scheduled_for, created_at, updated_at
		FROM payouts
		WHERE id = $1 AND tenant_id = $2`

	var p domain.Payout
	var openpayTxID, description *string

	err := r.db.QueryRow(ctx, q, payoutID, tenantID).Scan(
		&p.ID, &p.TenantID, &openpayTxID,
		&p.Amount, &p.Currency, &p.Status,
		&description, &p.ScheduledFor,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	if openpayTxID != nil {
		p.OpenpayTransactionID = *openpayTxID
	}
	if description != nil {
		p.Description = *description
	}
	return &p, nil
}

func (r *PaymentRepo) UpdatePayoutStatus(ctx context.Context, id uuid.UUID, status domain.PayoutStatus) error {
	const q = `UPDATE payouts SET status = $1, updated_at = NOW() WHERE id = $2`
	tag, err := r.db.Exec(ctx, q, string(status), id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ── scan helpers ──────────────────────────────────────────────────────────────

// paymentCols is the canonical SELECT column list — order must match the Scan
// calls in scanPayment and scanPaymentRow.
const paymentCols = `
	id, tenant_id, member_id, link_id, subscription_id,
	openpay_transaction_id, order_id, idempotency_key,
	gross_amount, openpay_fee, platform_fee, net_amount,
	currency, method, status, description,
	error_message, error_code,
	bank_clabe, bank_name, bank_reference, bank_agreement,
	metadata, created_at, updated_at`

func scanPayment(row pgx.Row) (*domain.Payment, error) {
	var p domain.Payment
	var linkID, subscriptionID *uuid.UUID
	var orderID, description, errMsg, errCode *string
	var bankCLABE, bankName, bankRef, bankAgr *string
	var metadata []byte

	err := row.Scan(
		&p.ID, &p.TenantID, &p.MemberID, &linkID, &subscriptionID,
		&p.OpenpayTransactionID, &orderID, &p.IdempotencyKey,
		&p.GrossAmount, &p.OpenpayFee, &p.PlatformFee, &p.NetAmount,
		&p.Currency, &p.Method, &p.Status, &description,
		&errMsg, &errCode,
		&bankCLABE, &bankName, &bankRef, &bankAgr,
		&metadata, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scan payment: %w", err)
	}
	applyNullable(&p, linkID, subscriptionID, orderID, description, errMsg, errCode, bankCLABE, bankName, bankRef, bankAgr, metadata)
	return &p, nil
}

func scanPaymentRow(rows pgx.Rows) (*domain.Payment, error) {
	var p domain.Payment
	var linkID, subscriptionID *uuid.UUID
	var orderID, description, errMsg, errCode *string
	var bankCLABE, bankName, bankRef, bankAgr *string
	var metadata []byte

	err := rows.Scan(
		&p.ID, &p.TenantID, &p.MemberID, &linkID, &subscriptionID,
		&p.OpenpayTransactionID, &orderID, &p.IdempotencyKey,
		&p.GrossAmount, &p.OpenpayFee, &p.PlatformFee, &p.NetAmount,
		&p.Currency, &p.Method, &p.Status, &description,
		&errMsg, &errCode,
		&bankCLABE, &bankName, &bankRef, &bankAgr,
		&metadata, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan payment row: %w", err)
	}
	applyNullable(&p, linkID, subscriptionID, orderID, description, errMsg, errCode, bankCLABE, bankName, bankRef, bankAgr, metadata)
	return &p, nil
}

func applyNullable(
	p *domain.Payment,
	linkID, subscriptionID *uuid.UUID,
	orderID, description, errMsg, errCode *string,
	bankCLABE, bankName, bankRef, bankAgr *string,
	metadata []byte,
) {
	p.LinkID = linkID
	p.SubscriptionID = subscriptionID
	if orderID != nil {
		p.OrderID = *orderID
	}
	if description != nil {
		p.Description = *description
	}
	if errMsg != nil {
		p.ErrorMessage = *errMsg
	}
	if errCode != nil {
		p.ErrorCode = *errCode
	}
	if bankCLABE != nil {
		p.BankCLABE = *bankCLABE
	}
	if bankName != nil {
		p.BankName = *bankName
	}
	if bankRef != nil {
		p.BankReference = *bankRef
	}
	if bankAgr != nil {
		p.BankAgreement = *bankAgr
	}
	if len(metadata) > 0 {
		p.Metadata = json.RawMessage(metadata)
	}
}

// ── cursor helpers ────────────────────────────────────────────────────────────

func encodeCursor(t time.Time, id uuid.UUID) string {
	raw := fmt.Sprintf("%s|%s", t.UTC().Format(time.RFC3339Nano), id.String())
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(token string) (time.Time, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, uuid.Nil, fmt.Errorf("invalid cursor format")
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	uid, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	return t, uid, nil
}
