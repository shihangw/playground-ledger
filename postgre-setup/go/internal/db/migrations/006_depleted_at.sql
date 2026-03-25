-- +goose Up

-- Track when an account balance hits zero so the waterfall can skip it
-- without a wasted DB round-trip.  NULL means the account has funds.
ALTER TABLE accounts ADD COLUMN depleted_at TIMESTAMPTZ;

-- Index lets the waterfall filter WHERE depleted_at IS NULL efficiently.
CREATE INDEX idx_accounts_depleted ON accounts(id) WHERE depleted_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_accounts_depleted;
ALTER TABLE accounts DROP COLUMN depleted_at;
