-- +goose Up
-- +goose StatementBegin

-- Replace the single platform_fee_bps column with a richer fee structure.
--
--   platform_fee_percentage_bps  — percentage of gross in basis points (e.g. 150 = 1.5%).
--   platform_fee_fixed_centavos  — flat amount in centavos (e.g. 250 = $2.50 MXN).
--   fee_type                     — how the two components are applied:
--                                    'added'     → platform_fee = (gross × bps / 10000) + fixed
--                                                  net = gross − openpay_fee − platform_fee
--                                    'inclusive' → constant = (gross × bps / 10000) + fixed
--                                                  platform_fee = constant − openpay_fee  (can be negative)
--                                                  net = gross − constant
--
-- At least one of percentage_bps / fixed_centavos must be > 0 (enforced at the
-- application layer, not as a DB constraint to keep migrations simple).

ALTER TABLE tenants
    ADD COLUMN platform_fee_percentage_bps INT    NOT NULL DEFAULT 0,
    ADD COLUMN platform_fee_fixed_centavos BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN fee_type                    TEXT   NOT NULL DEFAULT 'added';

-- Carry existing basis-point fee forward as an 'added' percentage fee.
UPDATE tenants SET platform_fee_percentage_bps = platform_fee_bps;

-- Drop the old column.
ALTER TABLE tenants DROP COLUMN platform_fee_bps;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE tenants
    ADD COLUMN platform_fee_bps INT NOT NULL DEFAULT 0;

UPDATE tenants SET platform_fee_bps = platform_fee_percentage_bps;

ALTER TABLE tenants
    DROP COLUMN platform_fee_percentage_bps,
    DROP COLUMN platform_fee_fixed_centavos,
    DROP COLUMN fee_type;

-- +goose StatementEnd
