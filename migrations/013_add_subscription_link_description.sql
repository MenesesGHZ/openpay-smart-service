-- +goose Up
ALTER TABLE subscription_links ADD COLUMN IF NOT EXISTS description TEXT;

-- +goose Down
ALTER TABLE subscription_links DROP COLUMN IF EXISTS description;
