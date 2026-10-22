-- name: PruneRequests :execrows
-- Drops idempotency rows older than the cutoff.
DELETE FROM mm_requests WHERE created_at < $1;
