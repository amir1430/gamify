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

-- ListAudit lists audit rows newest first; every filter is optional.
-- name: ListAudit :many
SELECT id, admin_id, action, target, before, after, reason, ip, created_at FROM admin_audit
WHERE (sqlc.narg(before_id)::bigint IS NULL OR id < sqlc.narg(before_id))
  AND (sqlc.narg(admin_id)::bigint IS NULL OR admin_id = sqlc.narg(admin_id))
  AND (sqlc.narg(action)::text IS NULL OR action = sqlc.narg(action))
  AND (sqlc.narg(target)::text IS NULL OR target = sqlc.narg(target))
ORDER BY id DESC LIMIT @max_rows;

-- ListAdjustments lists adjustments newest first; every filter is optional.
-- name: ListAdjustments :many
SELECT * FROM admin_adjustments
WHERE (sqlc.narg(before_id)::bigint IS NULL OR id < sqlc.narg(before_id))
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status))
  AND (sqlc.narg(user_id)::bigint IS NULL OR user_id = sqlc.narg(user_id))
ORDER BY id DESC LIMIT @max_rows;

-- ListAdmins lists the console users with only what the console shows: no hashes, no secrets.
-- name: ListAdmins :many
SELECT id, email, role, telegram_id, (disabled_at IS NOT NULL)::bool AS disabled,
       (totp_secret IS NOT NULL)::bool AS totp_enrolled, created_at
FROM admin_users ORDER BY id;

-- LockEnabledOwners locks every enabled owner (in id order) while a change is checked against
-- the last-owner rule.
-- name: LockEnabledOwners :many
SELECT id FROM admin_users WHERE role = 'owner' AND disabled_at IS NULL ORDER BY id FOR UPDATE;

-- name: LockUser :one
SELECT * FROM admin_users WHERE id = $1 FOR UPDATE;

-- name: SetUserAccess :exec
UPDATE admin_users SET role = @role,
    disabled_at = CASE WHEN @disabled::bool THEN coalesce(disabled_at, now()) ELSE NULL END
WHERE id = @id;

-- ResetTOTP makes the next login enrol again.
-- name: ResetTOTP :exec
UPDATE admin_users SET totp_secret = NULL, totp_pending = NULL, totp_last_step = 0 WHERE id = $1;

-- name: SetPasswordHash :exec
UPDATE admin_users SET password_hash = $2 WHERE id = $1;

-- ChangePasswordHash replaces the hash only if it is still the one the old password was checked
-- against: 0 rows means it changed meanwhile.
-- name: ChangePasswordHash :execrows
UPDATE admin_users SET password_hash = @new_hash WHERE id = @id AND password_hash = @old_hash;

-- name: DeleteSessionsOf :exec
DELETE FROM admin_sessions WHERE admin_id = $1;

-- name: DeleteOtherSessions :exec
DELETE FROM admin_sessions WHERE admin_id = @admin_id AND id <> @keep;

-- InsertAdjustment returns no row when the idempotency key is taken: the caller reads that one.
-- name: InsertAdjustment :one
INSERT INTO admin_adjustments (user_id, currency, amount, reason, requested_by, status, idempotency_key)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING *;

-- name: GetAdjustmentByKey :one
SELECT * FROM admin_adjustments WHERE idempotency_key = $1;

-- name: GetAdjustment :one
SELECT * FROM admin_adjustments WHERE id = $1;

-- name: LockAdjustment :one
SELECT * FROM admin_adjustments WHERE id = $1 FOR UPDATE;

-- DecideAdjustment records an approval or rejection of a pending adjustment.
-- name: DecideAdjustment :one
UPDATE admin_adjustments SET status = @status, approved_by = @approved_by
WHERE id = @id AND status = 'pending_approval'
RETURNING *;

-- FinishAdjustment marks an applied (done) or refused (failed) adjustment: 0 rows means it isn't
-- approved any more (another apply finished it).
-- name: FinishAdjustment :execrows
UPDATE admin_adjustments SET status = @status, error = sqlc.narg(error), done_at = now()
WHERE id = @id AND status = 'approved';

-- DueAdjustments are approved adjustments a crash may have left unapplied, oldest first.
-- name: DueAdjustments :many
SELECT * FROM admin_adjustments
WHERE status = 'approved' AND created_at < now() - interval '10 seconds'
ORDER BY created_at, id LIMIT 100;

-- CountAdjustments counts adjustments per status (statuses without any are left out).
-- name: CountAdjustments :many
SELECT status, count(*) AS n FROM admin_adjustments GROUP BY status;
