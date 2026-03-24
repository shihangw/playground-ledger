-- +goose Up

-- Users table
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    external_id VARCHAR(255) NOT NULL UNIQUE,  
    email VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Accounts (one per currency per user)
CREATE TABLE accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    currency VARCHAR(8) NOT NULL,
    balance DECIMAL(28, 12) NOT NULL DEFAULT 0,
    pending_balance DECIMAL(28, 12) NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (user_id, currency),
    CONSTRAINT balance_non_negative CHECK (balance >= 0)
);

-- Transactions (groups ledger entries)
CREATE TABLE transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_type VARCHAR(32) NOT NULL,  -- TRANSFER, DEPOSIT, WITHDRAWAL, FEE, SUBSCRIPTION
    status VARCHAR(16) NOT NULL DEFAULT 'COMPLETED',

    -- Parties
    source_account_id UUID REFERENCES accounts(id),
    destination_account_id UUID REFERENCES accounts(id),

    -- Amount
    amount DECIMAL(28, 12) NOT NULL,
    currency VARCHAR(8) NOT NULL,

    -- Platform fee (for marketplace transactions)
    fee_amount DECIMAL(28, 12) DEFAULT 0,
    fee_account_id UUID REFERENCES accounts(id),

    -- Idempotency
    idempotency_key UUID NOT NULL UNIQUE,

    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Ledger Entries (immutable, double-entry)
CREATE TABLE ledger_entries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id),

    -- Double-entry fields
    entry_type VARCHAR(32) NOT NULL,  -- DEBIT, CREDIT
    amount DECIMAL(28, 12) NOT NULL,  -- Always positive
    balance_after DECIMAL(28, 12) NOT NULL,

    -- Transaction grouping
    transaction_id UUID NOT NULL REFERENCES transactions(id),

    -- Metadata
    description TEXT,
    metadata JSONB,

    -- Idempotency
    idempotency_key UUID NOT NULL UNIQUE,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Indexes for hot paths
CREATE INDEX idx_accounts_user_currency ON accounts(user_id, currency);
CREATE INDEX idx_ledger_entries_account ON ledger_entries(account_id, created_at DESC);
CREATE INDEX idx_ledger_entries_transaction ON ledger_entries(transaction_id);
CREATE INDEX idx_transactions_idempotency ON transactions(idempotency_key);

-- +goose Down
DROP TABLE IF EXISTS ledger_entries;
DROP TABLE IF EXISTS transactions;
DROP TABLE IF EXISTS accounts;
DROP TABLE IF EXISTS users;
