-- name: ListGameSettings :many
SELECT * FROM game_settings;

-- name: ListGameLimits :many
SELECT * FROM game_limits;

-- Admin writes (internal/admin), each in the transaction that also writes the audit row.
-- name: LockGameSetting :one
SELECT * FROM game_settings WHERE game = $1 FOR UPDATE;

-- name: InsertGameSetting :one
INSERT INTO game_settings (game, enabled) VALUES ($1, $2)
ON CONFLICT (game) DO NOTHING
RETURNING *;

-- name: UpdateGameSetting :one
UPDATE game_settings SET enabled = $2, version = version + 1, updated_at = now()
WHERE game = $1
RETURNING *;

-- name: LockGameLimit :one
SELECT * FROM game_limits WHERE game = $1 AND currency = $2 AND variant = $3 FOR UPDATE;

-- name: InsertGameLimit :one
INSERT INTO game_limits (game, currency, variant, enabled, min_bet, max_bet, max_payout)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (game, currency, variant) DO NOTHING
RETURNING *;

-- name: UpdateGameLimit :one
UPDATE game_limits SET enabled = $4, min_bet = $5, max_bet = $6, max_payout = $7, version = version + 1, updated_at = now()
WHERE game = $1 AND currency = $2 AND variant = $3
RETURNING *;

-- name: DeleteGameLimit :exec
DELETE FROM game_limits WHERE game = $1 AND currency = $2 AND variant = $3;

-- name: ListGameLimitsFor :many
SELECT * FROM game_limits WHERE game = $1 AND currency = $2;
