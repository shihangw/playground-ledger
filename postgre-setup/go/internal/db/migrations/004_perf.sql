-- +goose Up
-- Remove overdraw constraint — allow negative balances
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS balance_non_negative;

-- Decouple ledger_entries from transactions table so the hot path doesn't need to write transactions
ALTER TABLE ledger_entries DROP CONSTRAINT IF EXISTS ledger_entries_transaction_id_fkey;
ALTER TABLE ledger_entries ALTER COLUMN transaction_id DROP NOT NULL;

-- Drop idempotency key constraints on ledger_entries — not needed in hot path
ALTER TABLE ledger_entries DROP CONSTRAINT IF EXISTS ledger_entries_idempotency_key_key;
ALTER TABLE ledger_entries ALTER COLUMN idempotency_key DROP NOT NULL;

-- +goose Down
ALTER TABLE ledger_entries ALTER COLUMN idempotency_key SET NOT NULL;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_idempotency_key_key UNIQUE (idempotency_key);
ALTER TABLE ledger_entries ALTER COLUMN transaction_id SET NOT NULL;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions(id);
ALTER TABLE accounts ADD CONSTRAINT balance_non_negative CHECK (balance >= 0);
