-- +goose Up
-- Feature flags operators create in the console (flags package). key is what game code reads and
-- never changes; id is the console's row ID. type is 'bool' only for now.
CREATE TABLE flags (
    id          bigserial   PRIMARY KEY,
    key         text        NOT NULL UNIQUE,
    description text        NOT NULL DEFAULT '',
    type        text        NOT NULL DEFAULT 'bool' CHECK (type IN ('bool')),
    enabled     boolean     NOT NULL DEFAULT false,
    percent     smallint    NOT NULL DEFAULT 0 CHECK (percent BETWEEN 0 AND 100),
    version     bigint      NOT NULL DEFAULT 1,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- Players who see a flag whenever it is on, whatever its percent.
CREATE TABLE flag_testers (
    flag_id bigint NOT NULL REFERENCES flags(id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id),
    PRIMARY KEY (flag_id, user_id)
);

-- +goose Down
DROP TABLE flag_testers;
DROP TABLE flags;
