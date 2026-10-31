-- +goose Up
-- Per-player restrictions set by operators. No row: nothing restricted. blocks names groups of RPC
-- methods the brand configures (restrict.Config.Blocks).
CREATE TABLE user_restrictions (
    user_id    bigint      PRIMARY KEY REFERENCES users(id),
    suspended  boolean     NOT NULL DEFAULT false,
    blocks     text[]      NOT NULL DEFAULT '{}',
    note       text        NOT NULL DEFAULT '',
    until      timestamptz,
    version    bigint      NOT NULL DEFAULT 1,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE user_restrictions;
