-- +goose Up

-- Lower number = higher priority (consumed first). Default 0.
ALTER TABLE credit_grants ADD COLUMN priority INT NOT NULL DEFAULT 0;

-- Replace partial index to include priority in ordering
DROP INDEX IF EXISTS idx_grants_account_active;
CREATE INDEX idx_grants_account_active ON credit_grants(account_id, priority ASC, expires_at ASC)
    WHERE status = 'ACTIVE';

-- +goose Down
DROP INDEX IF EXISTS idx_grants_account_active;
CREATE INDEX idx_grants_account_active ON credit_grants(account_id, expires_at ASC)
    WHERE status = 'ACTIVE';
ALTER TABLE credit_grants DROP COLUMN priority;
