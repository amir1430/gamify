-- +goose Up
-- Provably fair seed pairs: one active and one next per user.
CREATE TABLE seed_pairs (
    id               bigserial PRIMARY KEY,
    user_id          bigint      NOT NULL REFERENCES users(id),
    server_seed_enc  bytea       NOT NULL,
    key_id           text        NOT NULL,
    server_seed_hash bytea       NOT NULL,
    client_seed      text        NOT NULL,
    nonce            bigint      NOT NULL DEFAULT 0,
    status           text        NOT NULL CHECK (status IN ('next', 'active', 'revealed')),
    created_at       timestamptz NOT NULL DEFAULT now(),
    revealed_at      timestamptz
);
CREATE UNIQUE INDEX seed_pairs_one_active ON seed_pairs(user_id) WHERE status = 'active';
CREATE UNIQUE INDEX seed_pairs_one_next   ON seed_pairs(user_id) WHERE status = 'next';

-- +goose Down
DROP TABLE seed_pairs;
