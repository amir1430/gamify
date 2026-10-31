-- name: ListRestrictions :many
SELECT * FROM user_restrictions;

-- name: GetRestriction :one
SELECT * FROM user_restrictions WHERE user_id = $1;

-- Newest player first, for the console: before_id 0 is the first page.
-- name: PageRestrictions :many
SELECT * FROM user_restrictions
WHERE sqlc.arg(before_id)::bigint = 0 OR user_id < sqlc.arg(before_id)
ORDER BY user_id DESC
LIMIT sqlc.arg(lim);

-- Writes run in the console's transaction, which also writes the audit row.
-- name: LockRestriction :one
SELECT * FROM user_restrictions WHERE user_id = $1 FOR UPDATE;

-- name: UpsertRestriction :one
INSERT INTO user_restrictions (user_id, suspended, blocks, note, until)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (user_id) DO UPDATE SET suspended = EXCLUDED.suspended, blocks = EXCLUDED.blocks,
    note = EXCLUDED.note, until = EXCLUDED.until, version = user_restrictions.version + 1, updated_at = now()
RETURNING *;
