-- name: PruneRequests :execrows
-- Drops idempotency rows older than the cutoff.
DELETE FROM mm_requests WHERE created_at < $1;

-- name: GetRequest :one
SELECT queue, ticket_id, match_id, removed FROM mm_requests WHERE user_id = $1 AND idem_key = $2;

-- name: InsertRequest :execrows
-- 0 rows: the key was used before (a concurrent first call waits for the winner to commit).
INSERT INTO mm_requests (user_id, idem_key, queue) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING;

-- name: SetRequestTicket :exec
UPDATE mm_requests SET ticket_id = $3 WHERE user_id = $1 AND idem_key = $2;

-- name: SetRequestRemoved :exec
-- Records why the ticket went; the request keeps its ticket_id.
UPDATE mm_requests SET removed = $2 WHERE ticket_id = $1;

-- name: InsertTicket :one
INSERT INTO mm_tickets (queue, leader_id, party_id, currency, stake, properties, server, numbers, rating)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id, queued_at;

-- name: InsertTicketUser :exec
INSERT INTO mm_ticket_users (user_id, ticket_id) VALUES ($1, $2);

-- name: GetTicket :one
SELECT id, queue, leader_id, party_id, currency, stake, properties, server, numbers, rating, queued_at
FROM mm_tickets WHERE id = $1;

-- name: TicketUsers :many
SELECT user_id FROM mm_ticket_users WHERE ticket_id = $1 ORDER BY user_id;

-- name: UserTicketID :one
SELECT ticket_id FROM mm_ticket_users WHERE user_id = $1;

-- name: PartyTicketIDs :many
SELECT id FROM mm_tickets WHERE party_id = $1;

-- name: DeleteTicket :execrows
DELETE FROM mm_tickets WHERE id = $1;

-- name: UsersBusy :one
-- True if any user is in a non-final match of the queue.
SELECT EXISTS (
    SELECT 1 FROM mm_match_players p JOIN mm_matches m ON m.id = p.match_id
    WHERE p.user_id = ANY(sqlc.arg(user_ids)::bigint[]) AND m.queue = sqlc.arg(queue)::text
      AND m.status IN ('forming', 'running', 'finishing', 'cancelling'));

-- name: UserActiveMatches :many
SELECT m.id, m.queue, m.ref, m.status, m.currency, m.stake, m.fee_bp, m.result, m.reason, m.deadline, m.created_at
FROM mm_matches m WHERE m.status IN ('forming', 'running', 'finishing', 'cancelling')
  AND EXISTS (SELECT 1 FROM mm_match_players p WHERE p.match_id = m.id AND p.user_id = $1)
ORDER BY m.id;

-- name: MatchPlayers :many
SELECT id, match_id, user_id, team, party_id FROM mm_match_players WHERE match_id = ANY($1::bigint[]) ORDER BY id;

-- name: UserRatings :many
SELECT user_id, mu, sigma FROM mm_ratings WHERE key = sqlc.arg(key)::text AND user_id = ANY(sqlc.arg(user_ids)::bigint[]);

-- name: GetRating :one
SELECT mu, sigma, matches FROM mm_ratings WHERE user_id = $1 AND key = $2;
