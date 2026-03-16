-- +goose Up
-- +goose StatementBegin

-- audit_log is append-only; no UPDATE or DELETE is permitted on this table.
-- Enforce via a trigger or application-level policy.
CREATE TABLE audit_log (
    id            UUID        NOT NULL DEFAULT gen_random_uuid(),
    tenant_id     UUID        NOT NULL,   -- no FK — log survives tenant deletion
    actor         TEXT        NOT NULL,   -- API key prefix or "system"
    operation     TEXT        NOT NULL,   -- e.g. "CreateMember", "UpdatePaymentStatus"
    resource_type TEXT        NOT NULL,   -- e.g. "Member", "Payment"
    resource_id   TEXT        NOT NULL,
    payload       JSONB,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

-- Monthly partitions — create new ones in advance via cron or migration
CREATE TABLE audit_log_y2026m01 PARTITION OF audit_log
    FOR VALUES FROM ('2026-01-01') TO ('2026-02-01');
CREATE TABLE audit_log_y2026m02 PARTITION OF audit_log
    FOR VALUES FROM ('2026-02-01') TO ('2026-03-01');
CREATE TABLE audit_log_y2026m03 PARTITION OF audit_log
    FOR VALUES FROM ('2026-03-01') TO ('2026-04-01');
CREATE TABLE audit_log_y2026m04 PARTITION OF audit_log
    FOR VALUES FROM ('2026-04-01') TO ('2026-05-01');

CREATE INDEX idx_audit_log_tenant ON audit_log(tenant_id, created_at DESC);
CREATE INDEX idx_audit_log_resource ON audit_log(resource_type, resource_id);

-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS audit_log;
