-- name: CreateActiveSeedPair :exec
INSERT INTO seed_pairs (user_id, server_seed_enc, key_id, server_seed_hash, client_seed, status)
VALUES ($1, $2, $3, $4, $5, 'active')
ON CONFLICT (user_id) WHERE status = 'active' DO NOTHING;

-- name: CreateNextSeedPair :exec
INSERT INTO seed_pairs (user_id, server_seed_enc, key_id, server_seed_hash, client_seed, status)
VALUES ($1, $2, $3, $4, $5, 'next')
ON CONFLICT (user_id) WHERE status = 'next' DO NOTHING;

-- name: NextNonce :one
-- Call inside the bet's transaction; the row lock serializes bets and rotation per user.
UPDATE seed_pairs SET nonce = nonce + 1
WHERE user_id = $1 AND status = 'active'
RETURNING id, (nonce - 1)::bigint AS nonce, server_seed_enc, key_id, client_seed;

-- name: LockActiveSeedPair :one
SELECT id, server_seed_enc, key_id, server_seed_hash, client_seed, nonce
FROM seed_pairs
WHERE user_id = $1 AND status = 'active'
FOR UPDATE;

-- name: RevealSeedPair :exec
UPDATE seed_pairs SET status = 'revealed', revealed_at = now() WHERE id = $1;

-- name: PromoteNextSeedPair :one
UPDATE seed_pairs SET status = 'active', client_seed = $2
WHERE user_id = $1 AND status = 'next'
RETURNING server_seed_hash;

-- name: GetSeedInfo :many
SELECT status, server_seed_hash, client_seed, nonce
FROM seed_pairs
WHERE user_id = $1 AND status IN ('active', 'next');

-- name: GetSeedPairSecret :one
SELECT server_seed_enc, key_id, client_seed FROM seed_pairs WHERE id = $1 AND user_id = $2;

-- name: ListSeedPairs :many
-- revealed_enc is NULL unless the pair is revealed: an unrevealed seed never leaves the database.
SELECT id, status, server_seed_hash, client_seed, nonce, created_at, revealed_at, key_id,
       CASE WHEN status = 'revealed' THEN server_seed_enc END::bytea AS revealed_enc
FROM seed_pairs
WHERE user_id = sqlc.arg(user_id)
  AND (sqlc.narg(before_id)::bigint IS NULL OR id < sqlc.narg(before_id))
ORDER BY id DESC
LIMIT sqlc.arg(max_rows);
