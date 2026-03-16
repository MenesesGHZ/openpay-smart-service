-- +goose Up
-- +goose StatementBegin

-- api_key_prefix: first 12 chars of the raw API key, stored for display-only purposes.
-- The full key is never stored; only the SHA-256 hash (api_key_hash) is persisted.
ALTER TABLE tenants
    ADD COLUMN api_key_prefix TEXT NOT NULL DEFAULT '',
    ADD COLUMN deleted_at     TIMESTAMPTZ;

-- Partial index so soft-deleted tenants are invisible to normal lookups.
CREATE UNIQUE INDEX idx_tenants_api_key_hash_active
    ON tenants (api_key_hash)
    WHERE deleted_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_tenants_api_key_hash_active;

ALTER TABLE tenants
    DROP COLUMN IF EXISTS api_key_prefix,
    DROP COLUMN IF EXISTS deleted_at;

-- +goose StatementEnd
