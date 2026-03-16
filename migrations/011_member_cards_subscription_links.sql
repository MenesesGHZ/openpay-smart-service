-- +goose Up
-- +goose StatementBegin

-- member_cards table
-- Stores card metadata registered against a member via OpenPay JS SDK tokenization.
-- Raw card numbers are never stored here; only the openpay_card_id returned by the API.
CREATE TABLE member_cards (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    member_id        UUID NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    openpay_card_id  TEXT NOT NULL,
    card_type        TEXT,
    brand            TEXT,
    last_four        TEXT NOT NULL,
    holder_name      TEXT,
    expiration_year  TEXT,
    expiration_month TEXT,
    bank_name        TEXT,
    allows_charges   BOOLEAN NOT NULL DEFAULT TRUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX idx_member_cards_openpay ON member_cards(openpay_card_id);
CREATE INDEX idx_member_cards_member ON member_cards(tenant_id, member_id);

-- subscription_links table
-- Shareable tokens a tenant creates so a member can self-enroll in a plan.
-- The link is redeemed when the member provides a card token from the OpenPay JS SDK.
CREATE TABLE subscription_links (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    member_id       UUID NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    plan_id         UUID NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    token           TEXT NOT NULL UNIQUE,
    status          TEXT NOT NULL DEFAULT 'pending'
                      CHECK (status IN ('pending', 'completed', 'expired', 'cancelled')),
    subscription_id UUID REFERENCES subscriptions(id),
    expires_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at    TIMESTAMPTZ
);
CREATE INDEX idx_subscription_links_token  ON subscription_links(token);
CREATE INDEX idx_subscription_links_member ON subscription_links(tenant_id, member_id);

-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS subscription_links;
DROP TABLE IF EXISTS member_cards;
