-- +goose Up
-- Console users. role and permissions: admin.rolePerms.
CREATE TABLE admin_users (
    id             bigserial PRIMARY KEY,
    email          text        NOT NULL UNIQUE,  -- lowercased
    role           text        NOT NULL CHECK (role IN ('owner', 'operator', 'support', 'viewer')),
    password_hash  text        NOT NULL,         -- argon2id, PHC string
    totp_secret    bytea,                        -- sealed like seeds; NULL until enrolled
    telegram_id    bigint      UNIQUE,           -- optional Login Widget login
    disabled_at    timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    totp_pending   bytea,                        -- sealed; set during enrolment until confirmed
    totp_last_step bigint      NOT NULL DEFAULT 0 -- last accepted TOTP step (no replay)
);

-- Console sessions: the cookie's SHA-256, never the cookie.
CREATE TABLE admin_sessions (
    id         bigserial PRIMARY KEY,
    admin_id   bigint      NOT NULL REFERENCES admin_users(id),
    hash       bytea       NOT NULL UNIQUE,
    mfa        boolean     NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    seen_at    timestamptz NOT NULL DEFAULT now(),
    ip         inet,
    user_agent text
);

-- Append-only: no update or delete query exists for it.
CREATE TABLE admin_audit (
    id         bigserial PRIMARY KEY,
    admin_id   bigint      REFERENCES admin_users(id), -- NULL: failed login of an unknown email
    action     text        NOT NULL,   -- "game.limit.set", "user.unlink", "adjust.request", …
    target     text        NOT NULL,   -- "user:42", "game:arena/usd/", "adjust:7"
    before     jsonb,
    after      jsonb,
    reason     text,                   -- required for money and identity actions
    ip         inet,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX admin_audit_action_target ON admin_audit (action, target, created_at);

-- Balance adjustments: the only way the admin moves money (ledger.Adjust).
CREATE TABLE admin_adjustments (
    id           bigserial PRIMARY KEY,
    user_id      bigint      NOT NULL REFERENCES users(id),
    currency     text        NOT NULL,
    amount       bigint      NOT NULL CHECK (amount <> 0),  -- minor units; < 0 debits
    reason       text        NOT NULL,
    requested_by bigint      NOT NULL REFERENCES admin_users(id),
    approved_by  bigint      REFERENCES admin_users(id) CHECK (approved_by <> requested_by),
    status       text        NOT NULL CHECK (status IN ('pending_approval', 'approved', 'done', 'failed', 'rejected')),
    error        text,       -- 'failed': e.g. insufficient funds for a debit
    idempotency_key text     NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    done_at      timestamptz
);

-- +goose Down
DROP TABLE admin_adjustments;
DROP TABLE admin_audit;
DROP TABLE admin_sessions;
DROP TABLE admin_users;
