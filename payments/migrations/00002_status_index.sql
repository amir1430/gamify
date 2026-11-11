-- +goose Up
-- The console's alerts and lists count and filter by status and creation time.
CREATE INDEX pay_withdrawals_status_created ON pay_withdrawals (status, created_at);
CREATE INDEX pay_deposits_status_created ON pay_deposits (status, created_at);

-- +goose Down
DROP INDEX pay_deposits_status_created;
DROP INDEX pay_withdrawals_status_created;
