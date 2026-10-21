-- +goose Up
CREATE TABLE dice_bets (
    id              bigserial PRIMARY KEY,
    user_id         bigint      NOT NULL REFERENCES users(id),
    idempotency_key text        NOT NULL,
    seed_pair_id    bigint      NOT NULL REFERENCES seed_pairs(id),
    nonce           bigint      NOT NULL,
    amount          bigint      NOT NULL CHECK (amount >= 0), -- 0 = demo
    currency        text        NOT NULL,
    target          int         NOT NULL,  -- hundredths, 0..10000
    over            boolean     NOT NULL,
    roll            int         NOT NULL,  -- hundredths, 0..9999
    payout          bigint      NOT NULL,
    status          text        NOT NULL CHECK (status IN ('pending', 'settled', 'voided')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, idempotency_key),
    UNIQUE (seed_pair_id, nonce)
);
CREATE INDEX dice_bets_user ON dice_bets(user_id, id);

-- +goose Down
DROP TABLE dice_bets;
