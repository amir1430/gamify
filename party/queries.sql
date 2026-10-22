-- name: LockUserParties :exec
-- Serializes a user's joins and creates, so PartiesPerUser holds.
SELECT pg_advisory_xact_lock(hashtext('party:user'), sqlc.arg(user_id)::int);

-- name: CreateParty :one
INSERT INTO parties (leader_id, name, max_size, open) VALUES ($1, $2, $3, $4) RETURNING id;

-- name: GetParty :one
SELECT id, leader_id, name, max_size, open, COALESCE(locked_by, '')::text AS locked_by FROM parties WHERE id = $1;

-- name: LockParty :one
SELECT id, leader_id, name, max_size, open, COALESCE(locked_by, '')::text AS locked_by FROM parties WHERE id = $1 FOR UPDATE;

-- name: PartyMembers :many
SELECT user_id FROM party_members WHERE party_id = $1 ORDER BY joined_at, user_id;

-- name: UserPartyIDs :many
SELECT party_id FROM party_members WHERE user_id = $1 ORDER BY party_id;

-- name: AddMember :exec
INSERT INTO party_members (party_id, user_id) VALUES ($1, $2);

-- name: RemoveMember :execrows
DELETE FROM party_members WHERE party_id = $1 AND user_id = $2;

-- name: DeleteParty :exec
DELETE FROM parties WHERE id = $1;

-- name: SetLeader :exec
UPDATE parties SET leader_id = $2 WHERE id = $1;

-- name: SetLocked :exec
UPDATE parties SET locked_by = NULLIF($2::text, '') WHERE id = $1;

-- name: AddRequest :exec
INSERT INTO party_requests (party_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING;

-- name: DeleteRequest :execrows
DELETE FROM party_requests WHERE party_id = $1 AND user_id = $2;


-- name: ListParties :many
-- Newest first; user_id 0 is every party, else the parties the user is in.
SELECT id, leader_id, name, max_size, open, COALESCE(locked_by, '')::text AS locked_by FROM parties
WHERE (sqlc.arg(before_id)::bigint = 0 OR id < sqlc.arg(before_id))
  AND (sqlc.arg(user_id)::bigint = 0 OR EXISTS (
        SELECT 1 FROM party_members m WHERE m.party_id = parties.id AND m.user_id = sqlc.arg(user_id)))
ORDER BY id DESC LIMIT sqlc.arg(max_rows);
