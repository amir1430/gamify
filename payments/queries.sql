-- name: GetLimits :one
SELECT deposit, withdraw FROM pay_limits WHERE currency = $1 AND method = $2;

-- name: SetDepositLimits :exec
INSERT INTO pay_limits (currency, method, deposit) VALUES ($1, $2, $3)
ON CONFLICT (currency, method) DO UPDATE SET deposit = excluded.deposit, updated_at = now();

-- name: SetWithdrawLimits :exec
INSERT INTO pay_limits (currency, method, withdraw) VALUES ($1, $2, $3)
ON CONFLICT (currency, method) DO UPDATE SET withdraw = excluded.withdraw, updated_at = now();

-- name: OpenTarget :one
SELECT * FROM pay_targets WHERE method = $1 AND user_id = $2 AND invoice_id = '';

-- name: InsertTarget :one
INSERT INTO pay_targets (method, currency, user_id, address, invoice_id, amount)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT DO NOTHING
RETURNING *;

-- name: FindTarget :one
SELECT * FROM pay_targets
WHERE method = $1 AND ((invoice_id <> '' AND invoice_id = $3) OR (invoice_id = '' AND address <> '' AND address = $2))
ORDER BY invoice_id DESC
LIMIT 1;

-- name: InsertDeposit :one
INSERT INTO pay_deposits (method, currency, external_id, user_id, target_id, amount, status, reason)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (method, external_id) DO NOTHING
RETURNING *;

-- name: GetDeposit :one
SELECT * FROM pay_deposits WHERE method = $1 AND external_id = $2;

-- name: MarkCredited :one
UPDATE pay_deposits SET status = 'credited' WHERE id = $1 AND status = 'crediting' RETURNING *;

-- name: CreditingDeposits :many
SELECT * FROM pay_deposits WHERE status = 'crediting' ORDER BY id LIMIT 100;

-- name: LockUserWithdrawals :exec
-- Serializes one user's withdrawal requests (users 2^31-1 apart share a lock: harmless).
SELECT pg_advisory_xact_lock(hashtext('payments:withdraw'), (sqlc.arg(user_id)::bigint % 2147483647)::int);

-- name: WithdrawnSince :one
-- What counts against the daily cap: everything not refused, cancelled or failed.
SELECT COALESCE(sum(amount), 0)::bigint FROM pay_withdrawals
WHERE user_id = $1 AND currency = $2 AND created_at > now() - interval '24 hours'
  AND status NOT IN ('rejected', 'cancelled', 'failed');

-- name: InsertWithdrawal :one
INSERT INTO pay_withdrawals (user_id, currency, method, amount, fee, destination, idem_key, status)
VALUES ($1, $2, $3, $4, $5, $6, $7, 'holding')
RETURNING *;

-- name: WithdrawalByKey :one
SELECT * FROM pay_withdrawals WHERE user_id = $1 AND idem_key = $2;

-- name: GetWithdrawal :one
SELECT * FROM pay_withdrawals WHERE id = $1;

-- name: MoveWithdrawal :one
-- A status change from an expected status; no row: it already moved.
UPDATE pay_withdrawals SET status = sqlc.arg(status), settled = sqlc.arg(settled), reason = sqlc.arg(reason), updated_at = now()
WHERE id = sqlc.arg(id) AND status = sqlc.arg(from_status)
RETURNING *;

-- name: CancelWithdrawal :one
UPDATE pay_withdrawals SET status = 'cancelled', settled = false, updated_at = now()
WHERE id = $1 AND user_id = $2 AND status = 'requested'
RETURNING *;

-- name: MarkSettled :one
UPDATE pay_withdrawals SET settled = true, updated_at = now() WHERE id = $1 AND NOT settled RETURNING *;

-- name: HoldingWithdrawals :many
SELECT * FROM pay_withdrawals WHERE status = 'holding' ORDER BY id LIMIT 100;

-- name: UnsettledWithdrawals :many
SELECT * FROM pay_withdrawals WHERE NOT settled AND status IN ('paid', 'rejected', 'cancelled', 'failed') ORDER BY id LIMIT 100;

-- name: DueWithdrawals :many
-- Rows to send (approved) or to ask about (sending, its lease expired).
SELECT id FROM pay_withdrawals
WHERE NOT settled AND status IN ('approved', 'sending') AND lease_until < now()
ORDER BY id LIMIT 100;

-- name: ClaimWithdrawal :one
-- Takes the row's lease, so one worker at a time calls its Sender; no row: another holds it, or it moved.
UPDATE pay_withdrawals
SET status = 'sending', lease_until = now() + sqlc.arg(lease_us)::bigint * interval '1 microsecond', updated_at = now()
WHERE id = sqlc.arg(id) AND status = sqlc.arg(from_status) AND lease_until < now()
RETURNING *;

-- name: SendResult :one
-- Records the Sender's answer on a sending row (status stays 'sending' for a pending one); an empty ref keeps the old one.
UPDATE pay_withdrawals
SET status = sqlc.arg(status), ref = COALESCE(NULLIF(sqlc.arg(ref)::text, ''), ref), reason = sqlc.arg(reason), updated_at = now()
WHERE id = sqlc.arg(id) AND status = 'sending'
RETURNING *;
