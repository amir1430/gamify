-- +goose Up
-- Transfer timestamps for ledger.TransferReader (TigerBeetle's get_account_transfers): nanoseconds
-- since the UNIX epoch, unique and increasing in the order transfers were applied. ledger_clock
-- holds the last one handed out; a batch reserves its own under the row lock.
ALTER TABLE ledger_transfers ADD COLUMN ts bigint;

-- Existing transfers, in the order they were written: the commit time, nudged to stay unique.
-- +goose StatementBegin
DO $$
DECLARE
    r    record;
    last bigint := 0;
BEGIN
    FOR r IN SELECT id, created_at FROM ledger_transfers ORDER BY created_at, id LOOP
        last := greatest(last + 1, (extract(epoch FROM r.created_at) * 1000000000)::bigint);
        UPDATE ledger_transfers SET ts = last WHERE id = r.id;
    END LOOP;
END $$;
-- +goose StatementEnd

ALTER TABLE ledger_transfers ALTER COLUMN ts SET NOT NULL;
CREATE UNIQUE INDEX ledger_transfers_ts ON ledger_transfers (ts);
CREATE INDEX ledger_transfers_debit_ts ON ledger_transfers (debit, ts);
CREATE INDEX ledger_transfers_credit_ts ON ledger_transfers (credit, ts);
DROP INDEX ledger_transfers_debit;
DROP INDEX ledger_transfers_credit;

CREATE TABLE ledger_clock (
    one  boolean PRIMARY KEY DEFAULT true CHECK (one),
    last bigint  NOT NULL
);
INSERT INTO ledger_clock (last) SELECT coalesce(max(ts), 0) FROM ledger_transfers;

-- +goose Down
DROP TABLE ledger_clock;
CREATE INDEX ledger_transfers_debit ON ledger_transfers (debit);
CREATE INDEX ledger_transfers_credit ON ledger_transfers (credit);
DROP INDEX ledger_transfers_credit_ts;
DROP INDEX ledger_transfers_debit_ts;
ALTER TABLE ledger_transfers DROP COLUMN ts;
