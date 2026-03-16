-- +goose Up
-- +goose StatementBegin

-- Link payments to the subscription that triggered them.
-- NULL for one-time charges; non-NULL for all subscription-billed payments.
ALTER TABLE payments
    ADD COLUMN subscription_id UUID REFERENCES subscriptions(id);

CREATE INDEX idx_payments_subscription ON payments(subscription_id, created_at DESC)
    WHERE subscription_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
DROP INDEX IF EXISTS idx_payments_subscription;
ALTER TABLE payments DROP COLUMN IF EXISTS subscription_id;
