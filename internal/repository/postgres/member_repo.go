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

// MemberRepo implements repository.MemberRepository backed by PostgreSQL.
type MemberRepo struct{ db *pgxpool.Pool }

func NewMemberRepo(db *pgxpool.Pool) *MemberRepo { return &MemberRepo{db: db} }

const memberCols = `id, tenant_id, openpay_customer_id, external_id,
	name, email, phone, kyc_status,
	address_line1, address_line2, address_city, address_state,
	address_postal_code, address_country,
	created_at, updated_at`

// ── Create ────────────────────────────────────────────────────────────────────

func (r *MemberRepo) Create(ctx context.Context, m *domain.Member) error {
	if m.ID == uuid.Nil {
		m.ID = uuid.New()
	}
	now := time.Now()
	m.CreatedAt = now
	m.UpdatedAt = now

	_, err := r.db.Exec(ctx, `
		INSERT INTO members (
			id, tenant_id, openpay_customer_id, external_id,
			name, email, phone, kyc_status,
			address_line1, address_line2, address_city, address_state,
			address_postal_code, address_country,
			created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16
		)`,
		m.ID, m.TenantID, nilIfEmpty(m.OpenpayCustomerID), nilIfEmpty(m.ExternalID),
		m.Name, m.Email, nilIfEmpty(m.Phone), string(m.KYCStatus),
		nilIfEmpty(m.AddressLine1), nilIfEmpty(m.AddressLine2), nilIfEmpty(m.AddressCity),
		nilIfEmpty(m.AddressState), nilIfEmpty(m.AddressPostalCode), nilIfEmpty(m.AddressCountry),
		m.CreatedAt, m.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("member create: %w", err)
	}
	return nil
}

// ── GetByID ───────────────────────────────────────────────────────────────────

func (r *MemberRepo) GetByID(ctx context.Context, tenantID, memberID uuid.UUID) (*domain.Member, error) {
	row := r.db.QueryRow(ctx,
		`SELECT `+memberCols+` FROM members WHERE id=$1 AND tenant_id=$2`,
		memberID, tenantID,
	)
	m, err := scanMember(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("member.GetByID: %w", err)
	}
	return m, nil
}

// ── GetByExternalID ───────────────────────────────────────────────────────────

func (r *MemberRepo) GetByExternalID(ctx context.Context, tenantID uuid.UUID, externalID string) (*domain.Member, error) {
	row := r.db.QueryRow(ctx,
		`SELECT `+memberCols+` FROM members WHERE tenant_id=$1 AND external_id=$2`,
		tenantID, externalID,
	)
	m, err := scanMember(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("member.GetByExternalID: %w", err)
	}
	return m, nil
}

// ── Update ────────────────────────────────────────────────────────────────────

func (r *MemberRepo) Update(ctx context.Context, m *domain.Member) error {
	m.UpdatedAt = time.Now()
	tag, err := r.db.Exec(ctx, `
		UPDATE members SET
			openpay_customer_id = $1,
			external_id         = $2,
			name                = $3,
			email               = $4,
			phone               = $5,
			kyc_status          = $6,
			address_line1       = $7,
			address_line2       = $8,
			address_city        = $9,
			address_state       = $10,
			address_postal_code = $11,
			address_country     = $12,
			updated_at          = $13
		WHERE id=$14 AND tenant_id=$15`,
		nilIfEmpty(m.OpenpayCustomerID), nilIfEmpty(m.ExternalID),
		m.Name, m.Email, nilIfEmpty(m.Phone), string(m.KYCStatus),
		nilIfEmpty(m.AddressLine1), nilIfEmpty(m.AddressLine2), nilIfEmpty(m.AddressCity),
		nilIfEmpty(m.AddressState), nilIfEmpty(m.AddressPostalCode), nilIfEmpty(m.AddressCountry),
		m.UpdatedAt, m.ID, m.TenantID,
	)
	if err != nil {
		return fmt.Errorf("member.Update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ── List ──────────────────────────────────────────────────────────────────────

func (r *MemberRepo) List(ctx context.Context, opts repository.ListMembersOptions) ([]*domain.Member, string, error) {
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}

	args := []any{opts.TenantID}
	where := "tenant_id = $1"
	idx := 2

	if opts.Email != "" {
		where += fmt.Sprintf(" AND email = $%d", idx)
		args = append(args, opts.Email)
		idx++
	}
	if opts.KYCStatus != "" {
		where += fmt.Sprintf(" AND kyc_status = $%d", idx)
		args = append(args, opts.KYCStatus)
		idx++
	}
	if opts.CreatedAfter != nil {
		where += fmt.Sprintf(" AND created_at >= $%d", idx)
		args = append(args, *opts.CreatedAfter)
		idx++
	}
	if opts.CreatedBefore != nil {
		where += fmt.Sprintf(" AND created_at <= $%d", idx)
		args = append(args, *opts.CreatedBefore)
		idx++
	}
	if opts.PageToken != "" {
		ts, id, err := decodeCursor(opts.PageToken)
		if err == nil {
			where += fmt.Sprintf(" AND (created_at, id) < ($%d, $%d)", idx, idx+1)
			args = append(args, ts, id)
			idx += 2
		}
	}

	args = append(args, pageSize+1)
	q := fmt.Sprintf(
		`SELECT `+memberCols+` FROM members WHERE %s ORDER BY created_at DESC, id DESC LIMIT $%d`,
		where, idx,
	)

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("member.List: %w", err)
	}
	defer rows.Close()

	var members []*domain.Member
	for rows.Next() {
		m, err := scanMemberRows(rows)
		if err != nil {
			return nil, "", err
		}
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("member.List rows: %w", err)
	}

	var nextToken string
	if len(members) > pageSize {
		last := members[pageSize-1]
		nextToken = encodeCursor(last.CreatedAt, last.ID)
		members = members[:pageSize]
	}
	return members, nextToken, nil
}

// ── Payment links ─────────────────────────────────────────────────────────────

func (r *MemberRepo) CreatePaymentLink(ctx context.Context, l *domain.PaymentLink) error {
	if l.ID == uuid.Nil {
		l.ID = uuid.New()
	}
	now := time.Now()
	l.CreatedAt = now
	_, err := r.db.Exec(ctx, `
		INSERT INTO payment_links (id, tenant_id, member_id, token, amount, currency,
			description, order_id, status, expires_at, redeemed_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		l.ID, l.TenantID, l.MemberID, l.Token, l.Amount, l.Currency,
		nilIfEmpty(l.Description), nilIfEmpty(l.OrderID), string(l.Status),
		l.ExpiresAt, l.RedeemedAt, l.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("payment_link.Create: %w", err)
	}
	return nil
}

func (r *MemberRepo) GetPaymentLinkByToken(ctx context.Context, token string) (*domain.PaymentLink, error) {
	row := r.db.QueryRow(ctx,
		`SELECT id, tenant_id, member_id, token, amount, currency,
			description, order_id, status, expires_at, redeemed_at, created_at
		FROM payment_links WHERE token=$1`,
		token,
	)
	return scanPaymentLink(row)
}

func (r *MemberRepo) GetPaymentLinkByID(ctx context.Context, tenantID, linkID uuid.UUID) (*domain.PaymentLink, error) {
	row := r.db.QueryRow(ctx,
		`SELECT id, tenant_id, member_id, token, amount, currency,
			description, order_id, status, expires_at, redeemed_at, created_at
		FROM payment_links WHERE id=$1 AND tenant_id=$2`,
		linkID, tenantID,
	)
	return scanPaymentLink(row)
}

func (r *MemberRepo) UpdatePaymentLink(ctx context.Context, l *domain.PaymentLink) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE payment_links SET status=$1, expires_at=$2, redeemed_at=$3 WHERE id=$4`,
		string(l.Status), l.ExpiresAt, l.RedeemedAt, l.ID,
	)
	if err != nil {
		return fmt.Errorf("payment_link.Update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *MemberRepo) ListPaymentLinks(ctx context.Context, opts repository.ListPaymentLinksOptions) ([]*domain.PaymentLink, string, error) {
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}

	args := []any{opts.TenantID}
	where := "tenant_id = $1"
	idx := 2

	if opts.MemberID != nil {
		where += fmt.Sprintf(" AND member_id = $%d", idx)
		args = append(args, *opts.MemberID)
		idx++
	}
	if opts.Status != "" {
		where += fmt.Sprintf(" AND status = $%d", idx)
		args = append(args, opts.Status)
		idx++
	}
	if opts.PageToken != "" {
		ts, id, err := decodeCursor(opts.PageToken)
		if err == nil {
			where += fmt.Sprintf(" AND (created_at, id) < ($%d, $%d)", idx, idx+1)
			args = append(args, ts, id)
			idx += 2
		}
	}

	args = append(args, pageSize+1)
	q := fmt.Sprintf(
		`SELECT id, tenant_id, member_id, token, amount, currency,
			description, order_id, status, expires_at, redeemed_at, created_at
		FROM payment_links WHERE %s ORDER BY created_at DESC, id DESC LIMIT $%d`,
		where, idx,
	)

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("payment_links.List: %w", err)
	}
	defer rows.Close()

	var links []*domain.PaymentLink
	for rows.Next() {
		l, err := scanPaymentLinkRows(rows)
		if err != nil {
			return nil, "", err
		}
		links = append(links, l)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var nextToken string
	if len(links) > pageSize {
		last := links[pageSize-1]
		nextToken = encodeCursor(last.CreatedAt, last.ID)
		links = links[:pageSize]
	}
	return links, nextToken, nil
}

// ── scan helpers ──────────────────────────────────────────────────────────────

func scanMember(row pgx.Row) (*domain.Member, error) {
	var m domain.Member
	var openpayID, externalID, phone *string
	var addrLine1, addrLine2, addrCity, addrState, addrPostal, addrCountry *string
	var kycStatus string
	err := row.Scan(
		&m.ID, &m.TenantID, &openpayID, &externalID,
		&m.Name, &m.Email, &phone, &kycStatus,
		&addrLine1, &addrLine2, &addrCity, &addrState,
		&addrPostal, &addrCountry,
		&m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	applyMemberNullable(&m, openpayID, externalID, phone, kycStatus, addrLine1, addrLine2, addrCity, addrState, addrPostal, addrCountry)
	return &m, nil
}

func scanMemberRows(rows pgx.Rows) (*domain.Member, error) {
	var m domain.Member
	var openpayID, externalID, phone *string
	var addrLine1, addrLine2, addrCity, addrState, addrPostal, addrCountry *string
	var kycStatus string
	err := rows.Scan(
		&m.ID, &m.TenantID, &openpayID, &externalID,
		&m.Name, &m.Email, &phone, &kycStatus,
		&addrLine1, &addrLine2, &addrCity, &addrState,
		&addrPostal, &addrCountry,
		&m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	applyMemberNullable(&m, openpayID, externalID, phone, kycStatus, addrLine1, addrLine2, addrCity, addrState, addrPostal, addrCountry)
	return &m, nil
}

func applyMemberNullable(
	m *domain.Member,
	openpayID, externalID, phone *string,
	kycStatus string,
	addrLine1, addrLine2, addrCity, addrState, addrPostal, addrCountry *string,
) {
	if openpayID != nil {
		m.OpenpayCustomerID = *openpayID
	}
	if externalID != nil {
		m.ExternalID = *externalID
	}
	if phone != nil {
		m.Phone = *phone
	}
	m.KYCStatus = domain.KYCStatus(kycStatus)
	if addrLine1 != nil {
		m.AddressLine1 = *addrLine1
	}
	if addrLine2 != nil {
		m.AddressLine2 = *addrLine2
	}
	if addrCity != nil {
		m.AddressCity = *addrCity
	}
	if addrState != nil {
		m.AddressState = *addrState
	}
	if addrPostal != nil {
		m.AddressPostalCode = *addrPostal
	}
	if addrCountry != nil {
		m.AddressCountry = *addrCountry
	}
}

func scanPaymentLink(row pgx.Row) (*domain.PaymentLink, error) {
	var l domain.PaymentLink
	var description, orderID *string
	var status string
	err := row.Scan(
		&l.ID, &l.TenantID, &l.MemberID, &l.Token, &l.Amount, &l.Currency,
		&description, &orderID, &status, &l.ExpiresAt, &l.RedeemedAt, &l.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	l.Status = domain.PaymentLinkStatus(status)
	if description != nil {
		l.Description = *description
	}
	if orderID != nil {
		l.OrderID = *orderID
	}
	return &l, nil
}

func scanPaymentLinkRows(rows pgx.Rows) (*domain.PaymentLink, error) {
	var l domain.PaymentLink
	var description, orderID *string
	var status string
	err := rows.Scan(
		&l.ID, &l.TenantID, &l.MemberID, &l.Token, &l.Amount, &l.Currency,
		&description, &orderID, &status, &l.ExpiresAt, &l.RedeemedAt, &l.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	l.Status = domain.PaymentLinkStatus(status)
	if description != nil {
		l.Description = *description
	}
	if orderID != nil {
		l.OrderID = *orderID
	}
	return &l, nil
}
