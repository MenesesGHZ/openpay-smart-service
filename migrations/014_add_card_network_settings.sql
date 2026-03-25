-- +goose Up
-- +goose StatementBegin

-- card_networks_enabled: when TRUE, charges against cards whose brand appears
-- in card_network_list will be rejected before reaching OpenPay.
-- card_network_list: the list of denied card network brands, e.g. {"american_express","visa"}.
-- Brands are stored lower-case and matched case-insensitively at charge time.
ALTER TABLE tenants
    ADD COLUMN card_networks_enabled BOOLEAN  NOT NULL DEFAULT FALSE,
    ADD COLUMN card_network_list     TEXT[]   NOT NULL DEFAULT '{}';

-- +goose StatementEnd

-- +goose Down
ALTER TABLE tenants
    DROP COLUMN IF EXISTS card_networks_enabled,
    DROP COLUMN IF EXISTS card_network_list;
