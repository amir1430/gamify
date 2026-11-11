-- name: GetLimits :one
SELECT deposit, withdraw FROM pay_limits WHERE currency = $1 AND method = $2;

-- name: SetDepositLimits :exec
INSERT INTO pay_limits (currency, method, deposit) VALUES ($1, $2, $3)
ON CONFLICT (currency, method) DO UPDATE SET deposit = excluded.deposit, updated_at = now();

-- name: SetWithdrawLimits :exec
INSERT INTO pay_limits (currency, method, withdraw) VALUES ($1, $2, $3)
ON CONFLICT (currency, method) DO UPDATE SET withdraw = excluded.withdraw, updated_at = now();
