-- +goose Up
-- +goose StatementBegin

-- Add optional S3-backed logo URL to tenants.
-- NULL means no logo has been uploaded yet.
ALTER TABLE tenants
    ADD COLUMN IF NOT EXISTS logo_url TEXT;

-- +goose StatementEnd

-- +goose Down
ALTER TABLE tenants
    DROP COLUMN IF EXISTS logo_url;
