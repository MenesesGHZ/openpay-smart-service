-- +goose Up
-- +goose StatementBegin

-- Cards belong to Members (OpenPay customers), not to Tenants.
-- The tenant_card_configs table was based on the incorrect assumption that each
-- tenant had its own OpenPay merchant account. The service uses a single merchant
-- account; tenants receive funds via SPEI payout to their registered CLABE.
--
-- This migration is a no-op if tenant_card_configs was never created (fresh installs
-- with the corrected migration 001 will not have this table).

DROP TABLE IF EXISTS tenant_card_configs;

-- +goose StatementEnd

-- +goose Down
-- No rollback: the table was incorrect and should not be re-created.
-- If you need to re-introduce card storage, attach it to the members table instead.
