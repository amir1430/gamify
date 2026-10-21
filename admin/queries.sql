-- name: CreateUser :one
INSERT INTO admin_users (email, role, password_hash) VALUES ($1, $2, $3) RETURNING id;

-- name: GetUserByEmail :one
SELECT * FROM admin_users WHERE email = $1;

-- name: GetUser :one
SELECT * FROM admin_users WHERE id = $1;

-- name: GetUserByTelegramID :one
SELECT * FROM admin_users WHERE telegram_id = $1;

-- name: SetTOTPPending :exec
UPDATE admin_users SET totp_pending = $2 WHERE id = $1;

-- AcceptTOTPStep spends a step: 0 rows means it (or a later one) was used already.
-- name: AcceptTOTPStep :execrows
UPDATE admin_users SET totp_last_step = @step WHERE id = @id AND totp_last_step < @step;

-- ConfirmTOTP moves the pending secret the code was checked against into place: 0 rows means it
-- changed meanwhile (a second login started another enrolment).
-- name: ConfirmTOTP :execrows
UPDATE admin_users SET totp_secret = totp_pending, totp_pending = NULL, totp_last_step = @step
WHERE id = @id AND totp_pending = @pending;

-- LockLogin serialises login attempts for one lockout target ("email:…", "telegram:…") until the
-- transaction ends, so the count, the check and the audit row can't interleave.
-- name: LockLogin :exec
SELECT pg_advisory_xact_lock(hashtextextended(@target::text, 0));

-- CountLoginFails counts failures that lock a login: since the later of 15 minutes ago and the
-- user's last successful login. A missing TOTP (the UI's first step), a locked attempt and a bad
-- Telegram signature don't count.
-- name: CountLoginFails :one
SELECT count(*) FROM admin_audit f
WHERE f.action = 'login.fail' AND f.target = @target
  AND f.reason IN ('unknown', 'password', 'disabled', 'totp')
  AND f.created_at > greatest(now() - interval '15 minutes',
      coalesce((SELECT max(o.created_at) FROM admin_audit o
                WHERE o.action = 'login.ok' AND o.admin_id = sqlc.narg(admin_id)), '-infinity'));

-- name: InsertAudit :exec
INSERT INTO admin_audit (admin_id, action, target, before, after, reason, ip)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: CreateSession :one
INSERT INTO admin_sessions (admin_id, hash, mfa, ip, user_agent) VALUES ($1, $2, $3, $4, $5) RETURNING id;

-- GetSession reads a session by its cookie's hash with its user, and whether it timed out
-- (idle 30 min, absolute 12 h) or its seen_at is due a refresh (older than a minute).
-- name: GetSession :one
SELECT s.id, s.admin_id, s.mfa, u.email, u.role,
       (u.disabled_at IS NOT NULL)::bool AS disabled,
       (s.seen_at < now() - interval '30 minutes' OR s.created_at < now() - interval '12 hours')::bool AS expired,
       (s.seen_at < now() - interval '1 minute')::bool AS stale
FROM admin_sessions s JOIN admin_users u ON u.id = s.admin_id
WHERE s.hash = $1;

-- name: TouchSession :exec
UPDATE admin_sessions SET seen_at = now() WHERE id = $1;

-- name: DeleteSession :exec
DELETE FROM admin_sessions WHERE id = $1;
