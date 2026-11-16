-- name: ListFlags :many
SELECT * FROM flags ORDER BY key;

-- name: ListTesters :many
SELECT flag_id, user_id FROM flag_testers ORDER BY flag_id, user_id;

-- name: TestersOf :many
SELECT user_id FROM flag_testers WHERE flag_id = $1 ORDER BY user_id;

-- name: GetFlag :one
SELECT * FROM flags WHERE id = $1;

-- Writes run in the console's transaction, which also writes the audit row.
-- name: GetFlagForUpdate :one
SELECT * FROM flags WHERE id = $1 FOR UPDATE;

-- name: InsertFlag :one
INSERT INTO flags (key, description, type, enabled, percent)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateFlag :one
UPDATE flags SET description = $2, enabled = $3, percent = $4, version = version + 1, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: TouchFlag :one
UPDATE flags SET version = version + 1, updated_at = now() WHERE id = $1 RETURNING *;

-- name: DeleteTesters :exec
DELETE FROM flag_testers WHERE flag_id = $1;

-- name: InsertTesters :exec
INSERT INTO flag_testers (flag_id, user_id)
SELECT sqlc.arg(flag_id)::bigint, unnest(sqlc.arg(user_ids)::bigint[])
ON CONFLICT DO NOTHING;

-- name: DeleteFlag :one
DELETE FROM flags WHERE id = $1 RETURNING *;

-- The given IDs that aren't players, for a readable error.
-- name: MissingUsers :many
SELECT x.id::bigint AS id FROM unnest(sqlc.arg(ids)::bigint[]) AS x(id)
WHERE NOT EXISTS (SELECT 1 FROM users WHERE users.id = x.id)
ORDER BY x.id;
