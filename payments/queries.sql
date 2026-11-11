-- name: GetLimits :one
SELECT deposit, withdraw FROM pay_limits WHERE currency = $1 AND method = $2;

-- name: SetDepositLimits :exec
INSERT INTO pay_limits (currency, method, deposit) VALUES ($1, $2, $3)
ON CONFLICT (currency, method) DO UPDATE SET deposit = excluded.deposit, updated_at = now();

-- name: SetWithdrawLimits :exec
INSERT INTO pay_limits (currency, method, withdraw) VALUES ($1, $2, $3)
ON CONFLICT (currency, method) DO UPDATE SET withdraw = excluded.withdraw, updated_at = now();

-- name: OpenTarget :one
SELECT * FROM pay_targets WHERE method = $1 AND currency = $2 AND user_id = $3 AND amount = 0;

-- name: InsertTarget :one
INSERT INTO pay_targets (method, currency, user_id, address, invoice_id, amount)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT DO NOTHING
RETURNING *;

-- name: FindTarget :one
SELECT * FROM pay_targets
WHERE method = $1 AND ((address <> '' AND address = $2) OR (invoice_id <> '' AND invoice_id = $3))
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
