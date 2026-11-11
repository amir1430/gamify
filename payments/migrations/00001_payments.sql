-- +goose Up
-- Payments: per-method limits set in the console, deposit targets and deposits, withdrawals.
CREATE TABLE pay_limits (
    currency   text        NOT NULL,
    method     text        NOT NULL,
    deposit    jsonb,
    withdraw   jsonb,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (currency, method)
);

-- Where a user pays: one open address per (method, user), whatever the currency or amount, or a
-- one-off invoice (it has an invoice_id; the currency and amount checks apply to invoices only).
CREATE TABLE pay_targets (
    id         bigserial PRIMARY KEY,
    method     text        NOT NULL,
    currency   text        NOT NULL,
    user_id    bigint      NOT NULL REFERENCES users(id),
    address    text        NOT NULL DEFAULT '',
    invoice_id text        NOT NULL DEFAULT '',
    amount     bigint      NOT NULL DEFAULT 0 CHECK (amount >= 0),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX pay_targets_open ON pay_targets (method, user_id) WHERE invoice_id = '';
CREATE UNIQUE INDEX pay_targets_address ON pay_targets (method, address) WHERE invoice_id = '' AND address <> '';
CREATE UNIQUE INDEX pay_targets_invoice ON pay_targets (method, invoice_id) WHERE invoice_id <> '';

CREATE TABLE pay_deposits (
    id          bigserial PRIMARY KEY,
    method      text        NOT NULL,
    currency    text        NOT NULL,
    external_id text        NOT NULL,
    user_id     bigint      NOT NULL DEFAULT 0,
    target_id   bigint      NOT NULL DEFAULT 0,
    amount      bigint      NOT NULL CHECK (amount > 0),
    status      text        NOT NULL CHECK (status IN ('crediting', 'credited', 'ignored', 'unmatched')),
    reason      text        NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (method, external_id)
);

CREATE TABLE pay_withdrawals (
    id             bigserial PRIMARY KEY,
    user_id        bigint      NOT NULL REFERENCES users(id),
    currency       text        NOT NULL,
    method         text        NOT NULL,
    amount         bigint      NOT NULL CHECK (amount > 0),
    fee            bigint      NOT NULL DEFAULT 0 CHECK (fee >= 0),
    destination    text        NOT NULL,
    idem_key       text        NOT NULL,
    status         text        NOT NULL CHECK (status IN
        ('holding', 'requested', 'approved', 'sending', 'paid', 'rejected', 'cancelled', 'failed')),
    settled        boolean     NOT NULL DEFAULT false,
    ref            text        NOT NULL DEFAULT '',
    reason         text        NOT NULL DEFAULT '',
    first_approver bigint      NOT NULL DEFAULT 0,
    decided_by     bigint      NOT NULL DEFAULT 0,
    lease_until    timestamptz NOT NULL DEFAULT 'epoch',
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, idem_key)
);
CREATE INDEX pay_withdrawals_status ON pay_withdrawals (status) WHERE NOT settled;

