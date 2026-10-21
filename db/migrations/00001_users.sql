-- +goose Up
-- Players: an internal ID. How they log in is user_identities (auth providers).
CREATE TABLE users (
    id            bigserial PRIMARY KEY,
    username      text,
    display_name  text,
    language_code text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz
);

-- One row per way a player logs in: ("telegram", "12345"), ("custom", brand ID). A user has one or
-- more, at most one per provider (so the bot has one chat to write to); an identity belongs to
-- exactly one user.
CREATE TABLE user_identities (
    provider   text        NOT NULL,
    subject    text        NOT NULL,
    user_id    bigint      NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, subject),
    CONSTRAINT user_identities_one_per_provider UNIQUE (user_id, provider)
);

-- +goose Down
DROP TABLE user_identities;
DROP TABLE users;
