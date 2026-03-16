-- +goose Up
-- +goose StatementBegin

CREATE TABLE members (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id            UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    openpay_customer_id  TEXT        NOT NULL,
    external_id          TEXT,
    name                 TEXT        NOT NULL,
    email                TEXT        NOT NULL,
    phone                TEXT,
    kyc_status           TEXT        NOT NULL DEFAULT 'pending' CHECK (kyc_status IN ('pending', 'verified', 'rejected')),
    address_line1        TEXT,
    address_line2        TEXT,
    address_city         TEXT,
    address_state        TEXT,
    address_postal_code  TEXT,
    address_country      CHAR(2),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_members_openpay_id ON members(openpay_customer_id);
CREATE UNIQUE INDEX idx_members_external    ON members(tenant_id, external_id) WHERE external_id IS NOT NULL;
CREATE INDEX        idx_members_tenant_email ON members(tenant_id, email);
CREATE INDEX        idx_members_tenant       ON members(tenant_id);

CREATE TABLE payment_links (
    id          UUID              PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID              NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    member_id   UUID              NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    token       TEXT              NOT NULL UNIQUE,
    amount      BIGINT            NOT NULL CHECK (amount > 0),  -- minor units (centavos)
    currency    CHAR(3)           NOT NULL DEFAULT 'MXN',
    description TEXT,
    order_id    TEXT,
    status      TEXT              NOT NULL DEFAULT 'active'
                                    CHECK (status IN ('active', 'redeemed', 'expired', 'cancelled')),
    expires_at  TIMESTAMPTZ,
    redeemed_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ       NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_payment_links_tenant  ON payment_links(tenant_id, created_at DESC);
CREATE INDEX idx_payment_links_member  ON payment_links(member_id);
CREATE INDEX idx_payment_links_status  ON payment_links(status) WHERE status = 'active';

-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS payment_links;
DROP TABLE IF EXISTS members;
