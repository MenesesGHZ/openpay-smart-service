-- +goose Up
-- +goose StatementBegin

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- tenants: platform operators who use this service to collect payments.
--
-- Architecture note:
--   The service operator holds ONE OpenPay merchant account (stored in config,
--   not here). All customer charges land in that single merchant balance.
--   This service tracks per-tenant attribution in its own DB, and the scheduler
--   fires SPEI payouts to each tenant's registered bank_clabe.
CREATE TABLE tenants (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT        NOT NULL,
    api_key_hash    TEXT        NOT NULL UNIQUE,  -- SHA-256 of the tenant API key
    tier            TEXT        NOT NULL DEFAULT 'standard'
                                  CHECK (tier IN ('free', 'standard', 'enterprise')),

    -- Payout destination: where the scheduler sends this tenant's earned funds via SPEI.
    -- Stored AES-encrypted. NULL until the tenant calls SetupBankAccount.
    bank_clabe_enc  TEXT,        -- AES-256-GCM encrypted 18-digit CLABE
    bank_clabe_mask TEXT,        -- last 4 digits only, e.g. "**************1234"
    bank_holder_name TEXT,
    bank_name        TEXT,

    -- Service-owner platform fee applied on top of the OpenPay fee.
    -- Stored as integer basis points (1 BPS = 0.01%). Default: 150 = 1.5%.
    -- Per-tenant override; charged at payment settlement time.
    platform_fee_bps INTEGER     NOT NULL DEFAULT 150 CHECK (platform_fee_bps >= 0),

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE disbursement_schedules (
    tenant_id   UUID        PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    frequency   TEXT        NOT NULL DEFAULT 'daily'
                              CHECK (frequency IN ('daily', 'weekly', 'monthly', 'custom')),
    cron_expr   TEXT,        -- set when frequency = 'custom'
    enabled     BOOLEAN     NOT NULL DEFAULT TRUE,
    next_run_at TIMESTAMPTZ,
    last_run_at TIMESTAMPTZ,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS disbursement_schedules;
DROP TABLE IF EXISTS tenants;
