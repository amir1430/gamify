-- name: FindIdentity :one
SELECT user_id FROM user_identities WHERE provider = $1 AND subject = $2;

-- name: CreateUser :one
INSERT INTO users (username, display_name, language_code, last_login_at)
VALUES ($1, $2, $3, now())
RETURNING id;

-- name: TouchUser :exec
-- A login refreshes the names the provider sent; one it didn't send (NULL) is kept.
UPDATE users SET username = COALESCE(sqlc.narg(username), username),
  display_name = COALESCE(sqlc.narg(display_name), display_name),
  language_code = COALESCE(sqlc.narg(language_code), language_code), last_login_at = now()
WHERE id = sqlc.arg(id);

-- name: AddIdentity :execrows
-- 0 rows: the identity already belongs to someone (maybe this user). A second identity at the same
-- provider fails on user_identities_one_per_provider.
INSERT INTO user_identities (provider, subject, user_id) VALUES ($1, $2, $3)
ON CONFLICT (provider, subject) DO NOTHING;

-- name: GetSubject :one
SELECT subject FROM user_identities WHERE user_id = $1 AND provider = $2;

-- name: GetNames :many
SELECT id, COALESCE(display_name, username, '')::text AS name FROM users WHERE id = ANY(@ids::bigint[]);

-- Admin user list, newest first, keyset by id. exact matches the ID or any identity's subject;
-- pattern (already escaped, with %) the username or display name.
-- ponytail: ILIKE '%…%' scans users; add a trigram index if the list gets slow.
-- name: SearchUsers :many
SELECT id, username, display_name, created_at, last_login_at FROM users
WHERE (sqlc.narg(before_id)::bigint IS NULL OR id < sqlc.narg(before_id))
  AND (sqlc.narg(pattern)::text IS NULL
       OR username ILIKE sqlc.narg(pattern) OR display_name ILIKE sqlc.narg(pattern)
       OR id = sqlc.narg(exact_id)::bigint
       OR EXISTS (SELECT 1 FROM user_identities i WHERE i.user_id = users.id AND i.subject = sqlc.narg(exact)::text))
ORDER BY id DESC
LIMIT sqlc.arg(max_rows);

-- name: GetUsers :many
SELECT id, username, display_name, created_at, last_login_at FROM users
WHERE id = ANY(@ids::bigint[])
ORDER BY id DESC;

-- name: LockUser :one
-- Serializes changes to one user's identities (Unlink must never remove the last one).
SELECT id FROM users WHERE id = $1 FOR UPDATE;

-- name: CountIdentities :one
SELECT count(*) FROM user_identities WHERE user_id = $1;

-- name: DeleteIdentity :execrows
DELETE FROM user_identities WHERE user_id = $1 AND provider = $2;

-- name: ListIdentities :many
SELECT provider, subject, created_at FROM user_identities WHERE user_id = $1 ORDER BY provider;

-- UserStats counts players: all, signed up and logged in within the last day, week and month.
-- ponytail: scans users; add an index on created_at / last_login_at if the dashboard gets slow.
-- name: UserStats :one
SELECT count(*) AS total,
  count(*) FILTER (WHERE created_at > now() - interval '1 day') AS new_1d,
  count(*) FILTER (WHERE created_at > now() - interval '7 days') AS new_7d,
  count(*) FILTER (WHERE created_at > now() - interval '30 days') AS new_30d,
  count(*) FILTER (WHERE last_login_at > now() - interval '1 day') AS active_1d,
  count(*) FILTER (WHERE last_login_at > now() - interval '7 days') AS active_7d,
  count(*) FILTER (WHERE last_login_at > now() - interval '30 days') AS active_30d
FROM users;

-- SignupsByDay counts sign-ups per UTC day for the last days days, oldest first, today last,
-- days without any included as 0.
-- name: SignupsByDay :many
SELECT d::date AS day, count(u.id) AS signups
FROM generate_series(date_trunc('day', now() AT TIME ZONE 'UTC') - (sqlc.arg(days)::int - 1) * interval '1 day',
                     date_trunc('day', now() AT TIME ZONE 'UTC'), interval '1 day') AS d
LEFT JOIN users u ON u.created_at >= (d AT TIME ZONE 'UTC') AND u.created_at < ((d + interval '1 day') AT TIME ZONE 'UTC')
GROUP BY d ORDER BY d;
