-- +goose Up
-- +goose StatementBegin

-- Plans define recurring billing cycles created by a tenant.
-- Each plan maps 1:1 to an OpenPay Plan object under the shared merchant account.
-- The tenant_id enforces logical ownership; a tenant can only see/use their own plans.
CREATE TABLE plans (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id            UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    -- openpay_plan_id is the ID returned by OpenPay when the plan was created.
    openpay_plan_id      TEXT        NOT NULL,
    name                 TEXT        NOT NULL,
    -- amount in centavos (minor currency units)
    amount               BIGINT      NOT NULL CHECK (amount > 0),
    currency             CHAR(3)     NOT NULL DEFAULT 'MXN',
    -- billing cadence: every <repeat_every> <repeat_unit>
    repeat_every         SMALLINT    NOT NULL DEFAULT 1 CHECK (repeat_every > 0),
    repeat_unit          TEXT        NOT NULL CHECK (repeat_unit IN ('week', 'month', 'year')),
    -- how many days to give as a free trial before the first charge
    trial_days           SMALLINT    NOT NULL DEFAULT 0 CHECK (trial_days >= 0),
    -- on charge failure, retry this many times before applying status_on_retry_end
    retry_times          SMALLINT    NOT NULL DEFAULT 3 CHECK (retry_times >= 0),
    -- what to do with the subscription when retries are exhausted
    status_on_retry_end  TEXT        NOT NULL DEFAULT 'unpaid'
                                       CHECK (status_on_retry_end IN ('cancelled', 'unpaid')),
    active               BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_plans_openpay_id ON plans(openpay_plan_id);
CREATE INDEX        idx_plans_tenant     ON plans(tenant_id, active, created_at DESC);

-- Subscriptions link a member to a plan for automatic recurring billing.
-- Each subscription maps to an OpenPay Subscription object:
--   POST /v1/{merchantId}/customers/{customerId}/subscriptions
--
-- OpenPay charges the member's stored card at each period_end_date.
-- Webhook events subscription.charge.succeeded / subscription.charge.failed
-- fire when the automatic charge completes or fails.
CREATE TABLE subscriptions (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id            UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    member_id            UUID        NOT NULL REFERENCES members(id),
    plan_id              UUID        NOT NULL REFERENCES plans(id),
    openpay_sub_id       TEXT        NOT NULL,
    -- source_card_id is the OpenPay card ID used for automatic billing
    source_card_id       TEXT        NOT NULL,
    status               TEXT        NOT NULL DEFAULT 'active'
                                       CHECK (status IN ('trial','active','past_due','unpaid','cancelled','expired')),
    -- trial_end_date: NULL if no trial; populated from OpenPay's response
    trial_end_date       DATE,
    -- period_end_date: end of the current billing period (next charge date)
    period_end_date      DATE,
    -- cancel_at_period_end: true after CancelSubscription is called, subscription
    -- remains active until period_end_date then transitions to cancelled
    cancel_at_period_end BOOLEAN     NOT NULL DEFAULT FALSE,
    -- failed_charge_count: incremented by webhook on each failed attempt; reset on success
    failed_charge_count  SMALLINT    NOT NULL DEFAULT 0 CHECK (failed_charge_count >= 0),
    -- last_charge_id: the payments.id of the most recent charge for this subscription
    last_charge_id       UUID        REFERENCES payments(id),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_subscriptions_openpay_id ON subscriptions(openpay_sub_id);
CREATE INDEX        idx_subscriptions_tenant      ON subscriptions(tenant_id, status, created_at DESC);
CREATE INDEX        idx_subscriptions_member      ON subscriptions(member_id, created_at DESC);
CREATE INDEX        idx_subscriptions_plan        ON subscriptions(plan_id, status);

-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS subscriptions;
DROP TABLE IF EXISTS plans;
