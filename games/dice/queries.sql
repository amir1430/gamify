-- name: InsertDiceBet :one
INSERT INTO dice_bets (user_id, idempotency_key, seed_pair_id, nonce, amount, target, over, roll, payout, status, currency)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'pending', $10)
ON CONFLICT (user_id, idempotency_key) DO NOTHING
RETURNING *;

-- name: GetDiceBetByKey :one
SELECT * FROM dice_bets WHERE user_id = $1 AND idempotency_key = $2;

-- name: SetDiceBetStatus :exec
UPDATE dice_bets SET status = $2 WHERE id = $1 AND status = 'pending';

-- name: ListStalePendingDiceBets :many
SELECT * FROM dice_bets
WHERE status = 'pending' AND created_at < now() - interval '10 seconds'
ORDER BY id
LIMIT 100;

-- name: ListDiceBets :many
-- Every user's bets, newest first (app.BetSource with userID 0, the admin's view).
SELECT * FROM dice_bets
WHERE (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status))
  AND (sqlc.narg(before_id)::bigint IS NULL OR id < sqlc.narg(before_id))
ORDER BY id DESC
LIMIT sqlc.arg(max_rows);

-- name: ListDiceBetsByUser :many
-- One user's bets, newest first, on dice_bets_user.
SELECT * FROM dice_bets
WHERE user_id = sqlc.arg(user_id)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status))
  AND (sqlc.narg(before_id)::bigint IS NULL OR id < sqlc.narg(before_id))
ORDER BY id DESC
LIMIT sqlc.arg(max_rows);
