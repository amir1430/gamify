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
UPDATE mm_requests SET removed = $2, match_id = 0 WHERE ticket_id = $1;

-- name: SetRequestsMatch :exec
UPDATE mm_requests SET match_id = $1 WHERE ticket_id = ANY(sqlc.arg(ticket_ids)::bigint[]);

-- name: MoveRequests :exec
-- A re-queued ticket's requests follow it to its new row.
UPDATE mm_requests SET ticket_id = sqlc.arg(new_id), match_id = 0 WHERE ticket_id = sqlc.arg(old_id);

-- name: InsertTicket :one
INSERT INTO mm_tickets (queue, leader_id, party_id, currency, stake, properties, server, numbers, rating)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id, queued_at;

-- name: InsertTicketUser :exec
INSERT INTO mm_ticket_users (user_id, ticket_id) VALUES ($1, $2);

-- name: RequeueTicket :one
INSERT INTO mm_tickets (queue, leader_id, party_id, currency, stake, properties, server, numbers, rating, queued_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id;

-- name: GetTicket :one
SELECT * FROM mm_tickets WHERE id = $1;

-- name: QueueTickets :many
-- Locks the queue's tickets: a concurrent cancel waits and then finds them matched.
SELECT * FROM mm_tickets WHERE queue = $1 ORDER BY queued_at, id FOR UPDATE;

-- name: TicketsUsers :many
SELECT user_id, ticket_id FROM mm_ticket_users WHERE ticket_id = ANY($1::bigint[]) ORDER BY user_id;

-- name: TryQueueLock :one
SELECT pg_try_advisory_xact_lock(hashtext('mm:' || sqlc.arg(queue)::text));

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
SELECT m.* FROM mm_matches m WHERE m.status IN ('forming', 'running', 'finishing', 'cancelling')
  AND EXISTS (SELECT 1 FROM mm_match_players p WHERE p.match_id = m.id AND p.user_id = $1)
ORDER BY m.id;

-- name: InsertMatch :one
INSERT INTO mm_matches (queue, ref, status, currency, stake, fee_bp, deadline, tickets)
VALUES ($1, $2, 'forming', $3, $4, $5, $6, $7) RETURNING id;

-- name: InsertMatchPlayer :exec
INSERT INTO mm_match_players (match_id, user_id, team, party_id, rating_mu, rating_sigma) VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetMatch :one
SELECT * FROM mm_matches WHERE id = $1;

-- name: LockMatch :one
-- The match while it has a step to take, unless another transaction is advancing it.
SELECT * FROM mm_matches WHERE id = $1 AND status IN ('forming', 'finishing', 'cancelling') FOR UPDATE SKIP LOCKED;

-- name: LockMatchWait :one
-- The match in any status, waiting for a transaction advancing it.
SELECT * FROM mm_matches WHERE id = $1 FOR UPDATE;

-- name: UnfinishedMatches :many
SELECT id FROM mm_matches WHERE status IN ('forming', 'finishing', 'cancelling') ORDER BY id;

-- name: TimedOutMatches :many
SELECT id FROM mm_matches WHERE status = 'running' AND deadline < now() ORDER BY id;

-- name: SetMatchCancelling :execrows
UPDATE mm_matches SET status = 'cancelling', reason = $2, tickets = $3, updated_at = now()
WHERE id = $1 AND status = 'forming';

-- name: SetMatchRunning :execrows
UPDATE mm_matches SET status = 'running', tickets = '[]', updated_at = now() WHERE id = $1 AND status = 'forming';

-- name: CancelMatch :execrows
-- Nothing is re-queued.
UPDATE mm_matches SET status = 'cancelling', reason = $2, tickets = '[]', updated_at = now()
WHERE id = $1 AND status IN ('forming', 'running');

-- name: SetMatchFinishing :execrows
UPDATE mm_matches SET status = 'finishing', result = $2, payouts = $3, refund = $4, updated_at = now()
WHERE id = $1 AND status = 'running';

-- name: SetMatchSettled :execrows
UPDATE mm_matches SET status = 'settled', on_finish_done = $2, updated_at = now() WHERE id = $1 AND status = 'finishing';

-- name: SetOnFinishDone :exec
UPDATE mm_matches SET on_finish_done = true, updated_at = now() WHERE id = $1 AND status = 'settled' AND NOT on_finish_done;

-- name: PendingOnFinish :many
SELECT id FROM mm_matches WHERE status = 'settled' AND NOT on_finish_done ORDER BY id;

-- name: SetMatchCancelled :execrows
UPDATE mm_matches SET status = 'cancelled', tickets = '[]', updated_at = now() WHERE id = $1 AND status = 'cancelling';

-- name: MatchPlayers :many
SELECT id, match_id, user_id, team, party_id FROM mm_match_players WHERE match_id = ANY($1::bigint[]) ORDER BY id;

-- name: UserRatings :many
SELECT user_id, mu, sigma FROM mm_ratings WHERE key = sqlc.arg(key)::text AND user_id = ANY(sqlc.arg(user_ids)::bigint[]);

-- name: GetRating :one
SELECT mu, sigma, matches FROM mm_ratings WHERE user_id = $1 AND key = $2;

-- name: EnsureRatings :exec
-- Gives the users without a rating under key the default one, so LockRatings locks every user.
INSERT INTO mm_ratings (user_id, key, mu, sigma)
SELECT u, sqlc.arg(key)::text, sqlc.arg(mu)::float8, sqlc.arg(sigma)::float8 FROM unnest(sqlc.arg(user_ids)::bigint[]) AS u
ORDER BY u
ON CONFLICT DO NOTHING;

-- name: LockRatings :many
SELECT user_id, mu, sigma FROM mm_ratings WHERE key = sqlc.arg(key)::text AND user_id = ANY(sqlc.arg(user_ids)::bigint[])
ORDER BY user_id FOR UPDATE;

-- name: SetRating :exec
UPDATE mm_ratings SET mu = $3, sigma = $4, matches = matches + 1, updated_at = now() WHERE user_id = $1 AND key = $2;

-- name: InsertDirectMatch :one
-- No rows: the (queue, ref) match exists already. No tickets: nothing is ever re-queued.
INSERT INTO mm_matches (queue, ref, status, currency, stake, fee_bp, deadline)
VALUES ($1, $2, 'forming', $3, $4, $5, $6) ON CONFLICT (queue, ref) DO NOTHING RETURNING id;

-- name: MatchByRefRow :one
SELECT * FROM mm_matches WHERE queue = $1 AND ref = $2;

-- name: LockQueue :exec
-- Blocks until the queue's formation pass (TryQueueLock) and other direct creates are done.
SELECT pg_advisory_xact_lock(hashtext('mm:' || sqlc.arg(queue)::text));

-- name: ListMatches :many
-- Newest first. user_id 0, queue '' and status '' are no filter; history keeps staked matches that
-- ended (settled or cancelled).
SELECT m.* FROM mm_matches m
WHERE (sqlc.arg(before_id)::bigint = 0 OR m.id < sqlc.arg(before_id))
  AND (sqlc.arg(queue)::text = '' OR m.queue = sqlc.arg(queue))
  AND (sqlc.arg(status)::text = '' OR m.status = sqlc.arg(status))
  AND (NOT sqlc.arg(history)::bool OR (m.stake > 0 AND m.status IN ('settled', 'cancelled')))
  AND (sqlc.arg(user_id)::bigint = 0 OR EXISTS (
        SELECT 1 FROM mm_match_players p WHERE p.match_id = m.id AND p.user_id = sqlc.arg(user_id)))
ORDER BY m.id DESC LIMIT sqlc.arg(max_rows);

-- name: QueueStats :many
SELECT queue, count(*)::bigint AS waiting, min(queued_at)::timestamptz AS oldest FROM mm_tickets GROUP BY queue;

-- name: ListRatings :many
-- The ratings of the max_rows highest user IDs below before_id (0: no bound), or of one user.
SELECT r.user_id, r.key, r.mu, r.sigma, r.matches, hashtextextended(r.user_id::text || ':' || r.key, 0)::bigint AS row_id FROM mm_ratings r
WHERE r.user_id IN (
    SELECT DISTINCT u.user_id FROM mm_ratings u
    WHERE (sqlc.arg(before_id)::bigint = 0 OR u.user_id < sqlc.arg(before_id))
      AND (sqlc.arg(user_id)::bigint = 0 OR u.user_id = sqlc.arg(user_id))
    ORDER BY u.user_id DESC LIMIT sqlc.arg(max_rows))
ORDER BY r.user_id DESC, r.key;
