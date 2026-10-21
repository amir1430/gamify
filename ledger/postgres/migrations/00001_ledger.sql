-- +goose Up
-- Double-entry ledger with TigerBeetle's semantics (see ledger.Driver). IDs are 128-bit: uuid
-- holds the 16 bytes, high half first. Amounts and totals are minor units.
CREATE TABLE ledger_accounts (
    id              uuid    PRIMARY KEY,
    ledger          bigint  NOT NULL,
    code            integer NOT NULL,
    no_overdraft    boolean NOT NULL,
    debits_pending  bigint  NOT NULL DEFAULT 0 CHECK (debits_pending >= 0),
    debits_posted   bigint  NOT NULL DEFAULT 0 CHECK (debits_posted >= 0),
    credits_pending bigint  NOT NULL DEFAULT 0 CHECK (credits_pending >= 0),
    credits_posted  bigint  NOT NULL DEFAULT 0 CHECK (credits_posted >= 0),
    -- The driver refuses an overdraft before writing; this catches a driver bug.
    CHECK (NOT no_overdraft OR debits_pending + debits_posted <= credits_posted)
);

CREATE TABLE ledger_transfers (
    id            uuid    PRIMARY KEY,
    debit         uuid    NOT NULL REFERENCES ledger_accounts(id),
    credit        uuid    NOT NULL REFERENCES ledger_accounts(id),
    amount        bigint  NOT NULL CHECK (amount >= 0),
    pending_id    uuid,                     -- the pending transfer a post or void finishes
    ledger        bigint  NOT NULL,
    code          integer NOT NULL,
    flags         integer NOT NULL,         -- 1 linked, 2 pending, 4 post, 8 void, 16 balancing debit
    pending_state text CHECK (pending_state IN ('pending', 'posted', 'voided')), -- pending transfers only
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ledger_transfers_debit ON ledger_transfers(debit);
CREATE INDEX ledger_transfers_credit ON ledger_transfers(credit);

-- IDs of transfers that failed for a reason that depends on state: reserved, never retried.
CREATE TABLE ledger_failed_transfers (
    id         uuid PRIMARY KEY,
    result     text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE ledger_failed_transfers;
DROP TABLE ledger_transfers;
DROP TABLE ledger_accounts;
