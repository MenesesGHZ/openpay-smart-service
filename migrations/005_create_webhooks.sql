-- +goose Up
-- +goose StatementBegin

CREATE TABLE webhook_subscriptions (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    url          TEXT        NOT NULL,
    secret_enc   TEXT        NOT NULL,                    -- AES-256-GCM encrypted HMAC secret
    events       TEXT[]      NOT NULL DEFAULT '{}',       -- e.g. '{charge.succeeded,payout.failed}'
    retry_policy JSONB       NOT NULL DEFAULT '{"max_attempts":7,"intervals_sec":[5,30,120,600,3600,21600,86400]}',
    enabled      BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_webhook_subs_tenant ON webhook_subscriptions(tenant_id);

CREATE TABLE webhook_deliveries (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    subscription_id   UUID        NOT NULL REFERENCES webhook_subscriptions(id) ON DELETE CASCADE,
    event_type        TEXT        NOT NULL,
    payload           JSONB       NOT NULL,
    status            TEXT        NOT NULL DEFAULT 'pending'
                                    CHECK (status IN ('pending', 'delivered', 'failed', 'dlq')),
    attempts          INT         NOT NULL DEFAULT 0,
    response_code     INT,
    latency_ms        BIGINT,
    error_message     TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_attempted_at TIMESTAMPTZ,
    next_retry_at     TIMESTAMPTZ
);

CREATE INDEX idx_webhook_del_sub     ON webhook_deliveries(subscription_id, created_at DESC);
CREATE INDEX idx_webhook_del_pending ON webhook_deliveries(status, next_retry_at)
    WHERE status IN ('pending', 'failed');

-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS webhook_deliveries;
DROP TABLE IF EXISTS webhook_subscriptions;
