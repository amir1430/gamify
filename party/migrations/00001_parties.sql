-- +goose Up
-- Parties: a leader, members in join order, join requests for closed parties.
CREATE TABLE parties (
    id         bigserial PRIMARY KEY,
    leader_id  bigint      NOT NULL REFERENCES users(id),
    name       text        NOT NULL DEFAULT '',
    max_size   int         NOT NULL CHECK (max_size >= 1),
    open       boolean     NOT NULL,
    locked_by  text,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE party_members (
    party_id  bigint      NOT NULL REFERENCES parties(id) ON DELETE CASCADE,
    user_id   bigint      NOT NULL REFERENCES users(id),
    joined_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (party_id, user_id)
);
CREATE INDEX party_members_user ON party_members (user_id);

CREATE TABLE party_requests (
    party_id   bigint      NOT NULL REFERENCES parties(id) ON DELETE CASCADE,
    user_id    bigint      NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (party_id, user_id)
);

-- +goose Down
DROP TABLE party_requests;
DROP TABLE party_members;
DROP TABLE parties;
