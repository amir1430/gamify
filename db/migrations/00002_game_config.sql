-- +goose Up
-- Admin-set game limits (gameconfig). No row = the game's defaults in code. Code defaults are also
-- hard caps: a row can only tighten them.
CREATE TABLE game_settings (
    game       text PRIMARY KEY,
    enabled    boolean     NOT NULL DEFAULT true,
    version    bigint      NOT NULL DEFAULT 1,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Overrides per game and currency, for the whole game (variant '') or one variant. A variant row
-- inherits what it leaves NULL from the game row.
CREATE TABLE game_limits (
    game       text        NOT NULL,
    currency   text        NOT NULL,
    variant    text        NOT NULL DEFAULT '',
    enabled    boolean     NOT NULL DEFAULT true,
    min_amount    bigint      CHECK (min_amount > 0),
    max_amount    bigint      CHECK (max_amount > 0),
    max_payout bigint      CHECK (max_payout > 0),
    version    bigint      NOT NULL DEFAULT 1,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (game, currency, variant)
);

-- +goose Down
DROP TABLE game_limits;
DROP TABLE game_settings;
