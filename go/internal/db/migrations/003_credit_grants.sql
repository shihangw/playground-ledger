-- +goose Up

-- Credit grants (expirable credit balances per account)
CREATE TABLE credit_grants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id),
    grant_type VARCHAR(32) NOT NULL,  -- SIGNUP_BONUS, PROMOTION, MANUAL
    initial_amount DECIMAL(28, 12) NOT NULL,
    remaining_amount DECIMAL(28, 12) NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'ACTIVE',  -- ACTIVE, EXPIRED, DEPLETED, REVOKED
    idempotency_key UUID NOT NULL UNIQUE,
    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT remaining_non_negative CHECK (remaining_amount >= 0),
    CONSTRAINT initial_positive CHECK (initial_amount > 0),
    CONSTRAINT valid_grant_status CHECK (status IN ('ACTIVE', 'EXPIRED', 'DEPLETED', 'REVOKED')),
    CONSTRAINT valid_grant_type CHECK (grant_type IN ('SIGNUP_BONUS', 'PROMOTION', 'MANUAL'))
);

-- Grant ledger (audit trail for drawdowns, expirations, revocations)
CREATE TABLE grant_ledger_entries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    grant_id UUID NOT NULL REFERENCES credit_grants(id),
    entry_type VARCHAR(16) NOT NULL,  -- DRAWDOWN, EXPIRATION, REVOCATION
    amount DECIMAL(28, 12) NOT NULL,
    remaining_after DECIMAL(28, 12) NOT NULL,
    description TEXT,
    idempotency_key UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT valid_entry_type CHECK (entry_type IN ('DRAWDOWN', 'EXPIRATION', 'REVOCATION'))
);

-- Active grants for an account (FIFO by expiration)
CREATE INDEX idx_grants_account_active ON credit_grants(account_id, expires_at ASC)
    WHERE status = 'ACTIVE';

-- Expiration sweep
CREATE INDEX idx_grants_expiration ON credit_grants(expires_at)
    WHERE status = 'ACTIVE';

-- Grant ledger by grant
CREATE INDEX idx_grant_ledger_grant ON grant_ledger_entries(grant_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS grant_ledger_entries;
DROP TABLE IF EXISTS credit_grants;
