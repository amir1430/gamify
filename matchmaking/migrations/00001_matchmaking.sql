-- +goose Up
-- Matchmaking: waiting tickets, matches and their players, ratings.
CREATE TABLE mm_tickets (
    id         bigserial PRIMARY KEY,
    queue      text        NOT NULL,
    leader_id  bigint      NOT NULL REFERENCES users(id),
    party_id   bigint      NOT NULL DEFAULT 0,
    currency   text        NOT NULL DEFAULT '',
    stake      bigint      NOT NULL DEFAULT 0 CHECK (stake >= 0),
    properties jsonb       NOT NULL DEFAULT '{}',
    server     jsonb       NOT NULL DEFAULT '{}',
    numbers    jsonb       NOT NULL DEFAULT '{}',
    rating     float8      NOT NULL DEFAULT 0,
    queued_at  timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX mm_tickets_queue ON mm_tickets (queue, queued_at);

-- One ticket per user, across queues.
CREATE TABLE mm_ticket_users (
    user_id   bigint PRIMARY KEY REFERENCES users(id),
    ticket_id bigint NOT NULL REFERENCES mm_tickets(id) ON DELETE CASCADE
);
CREATE INDEX mm_ticket_users_ticket ON mm_ticket_users (ticket_id);

-- Idempotency of queue and cancel calls.
CREATE TABLE mm_requests (
    user_id    bigint      NOT NULL REFERENCES users(id),
    idem_key   text        NOT NULL,
    queue      text        NOT NULL,
    ticket_id  bigint      NOT NULL DEFAULT 0,
    match_id   bigint      NOT NULL DEFAULT 0,
    removed    text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, idem_key)
);

CREATE TABLE mm_matches (
    id         bigserial PRIMARY KEY,
    queue      text        NOT NULL,
    ref        text        NOT NULL,
    status     text        NOT NULL,
    currency   text        NOT NULL DEFAULT '',
    stake      bigint      NOT NULL DEFAULT 0 CHECK (stake >= 0),
    fee_bp     bigint      NOT NULL DEFAULT 0,
    result     jsonb,
    reason     text        NOT NULL DEFAULT '',
    deadline   timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (queue, ref)
);
CREATE INDEX mm_matches_status ON mm_matches (status, deadline);

-- id is the pot entry ID.
CREATE TABLE mm_match_players (
    id           bigserial PRIMARY KEY,
    match_id     bigint NOT NULL REFERENCES mm_matches(id),
    user_id      bigint NOT NULL REFERENCES users(id),
    team         int    NOT NULL,
    party_id     bigint NOT NULL DEFAULT 0,
    rating_mu    float8 NOT NULL,
    rating_sigma float8 NOT NULL,
    UNIQUE (match_id, user_id)
);
CREATE INDEX mm_match_players_user ON mm_match_players (user_id);

CREATE TABLE mm_ratings (
    user_id    bigint      NOT NULL REFERENCES users(id),
    key        text        NOT NULL,
    mu         float8      NOT NULL,
    sigma      float8      NOT NULL,
    matches    int         NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, key)
);
