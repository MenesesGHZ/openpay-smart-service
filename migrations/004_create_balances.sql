-- +goose Up
-- +goose StatementBegin

CREATE TABLE balances (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    member_id   UUID        REFERENCES members(id) ON DELETE CASCADE,
    available   BIGINT      NOT NULL DEFAULT 0,  -- minor units
    pending     BIGINT      NOT NULL DEFAULT 0,
    currency    CHAR(3)     NOT NULL DEFAULT 'MXN',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uq_balances_scope UNIQUE (tenant_id, member_id, currency)
);

CREATE INDEX idx_balances_tenant   ON balances(tenant_id, currency);
CREATE INDEX idx_balances_member   ON balances(member_id) WHERE member_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS balances;
