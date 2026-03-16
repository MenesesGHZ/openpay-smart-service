-- +goose Up
-- +goose StatementBegin

CREATE TABLE payments (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id               UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    member_id               UUID        NOT NULL REFERENCES members(id),
    link_id                 UUID        REFERENCES payment_links(id),
    openpay_transaction_id  TEXT        NOT NULL,
    order_id                TEXT,
    idempotency_key         TEXT        NOT NULL,
    -- Fee breakdown — all values in centavos (integer minor units).
    -- Populated when the charge.succeeded webhook is processed; 0 until then.
    --
    --   gross_amount  = total charged to the member (what OpenPay received)
    --   openpay_fee   = OpenPay's processing fee from charge.fee_details (actual value)
    --                   Card: ~2.9% + $2.50 MXN; OXXO: $10.00 MXN flat; SPEI: $0
    --   platform_fee  = gross_amount × tenant.platform_fee_bps / 10000  (service-owner cut)
    --   net_amount    = gross_amount − openpay_fee − platform_fee
    --                   This is what gets credited to the tenant's internal balance
    --                   and later sent via SPEI payout to their registered CLABE.
    --
    -- Example for a $500.00 MXN card charge:
    --   gross_amount = 50000, openpay_fee = 1700, platform_fee = 750, net_amount = 47550
    gross_amount            BIGINT      NOT NULL DEFAULT 0 CHECK (gross_amount >= 0),
    openpay_fee             BIGINT      NOT NULL DEFAULT 0 CHECK (openpay_fee >= 0),
    platform_fee            BIGINT      NOT NULL DEFAULT 0 CHECK (platform_fee >= 0),
    net_amount              BIGINT      NOT NULL DEFAULT 0 CHECK (net_amount >= 0),

    currency                CHAR(3)     NOT NULL DEFAULT 'MXN',
    method                  TEXT        NOT NULL CHECK (method IN ('card', 'bank_account', 'store')),
    status                  TEXT        NOT NULL DEFAULT 'pending'
                                          CHECK (status IN ('pending','in_progress','completed','failed','cancelled','refunded','chargeback')),
    description             TEXT,
    error_message           TEXT,
    error_code              TEXT,
    -- SPEI / bank transfer fields
    bank_clabe              TEXT,
    bank_name               TEXT,
    bank_reference          TEXT,
    bank_agreement          TEXT,
    -- Flexible metadata from OpenPay
    metadata                JSONB,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Idempotency: one successful payment per (tenant, idempotency_key)
CREATE UNIQUE INDEX idx_payments_idempotency    ON payments(tenant_id, idempotency_key);
CREATE UNIQUE INDEX idx_payments_openpay_tx     ON payments(openpay_transaction_id);
CREATE INDEX        idx_payments_tenant_status  ON payments(tenant_id, status, created_at DESC);
CREATE INDEX        idx_payments_member         ON payments(member_id, created_at DESC);
CREATE INDEX        idx_payments_order          ON payments(tenant_id, order_id) WHERE order_id IS NOT NULL;

CREATE TABLE payouts (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id               UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    openpay_transaction_id  TEXT,
    amount                  BIGINT      NOT NULL CHECK (amount > 0),
    currency                CHAR(3)     NOT NULL DEFAULT 'MXN',
    status                  TEXT        NOT NULL DEFAULT 'pending'
                                          CHECK (status IN ('pending','in_progress','completed','failed')),
    description             TEXT,
    scheduled_for           TIMESTAMPTZ,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_payouts_tenant_status ON payouts(tenant_id, status, created_at DESC);

-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS payouts;
DROP TABLE IF EXISTS payments;
