-- name: InsertAccount :execrows
INSERT INTO ledger_accounts (id, ledger, code, no_overdraft) VALUES ($1, $2, $3, $4)
ON CONFLICT (id) DO NOTHING;

-- name: GetAccount :one
SELECT * FROM ledger_accounts WHERE id = $1;

-- Locks in ID order, so two batches never wait on each other in a cycle.
-- name: LockAccounts :many
SELECT * FROM ledger_accounts WHERE id = ANY(@ids::uuid[]) ORDER BY id FOR UPDATE;

-- name: LookupAccounts :many
SELECT * FROM ledger_accounts WHERE id = ANY(@ids::uuid[]);

-- name: GetTransfers :many
SELECT * FROM ledger_transfers WHERE id = ANY(@ids::uuid[]);

-- name: GetFailed :many
SELECT id FROM ledger_failed_transfers WHERE id = ANY(@ids::uuid[]);

-- name: InsertTransfers :copyfrom
INSERT INTO ledger_transfers (id, debit, credit, amount, pending_id, ledger, code, flags, pending_state)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: InsertFailed :batchexec
INSERT INTO ledger_failed_transfers (id, result) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING;

-- name: SetPendingState :batchexec
UPDATE ledger_transfers SET pending_state = $2 WHERE id = $1;

-- name: UpdateAccount :batchexec
UPDATE ledger_accounts
SET debits_pending = $2, debits_posted = $3, credits_pending = $4, credits_posted = $5
WHERE id = $1;
