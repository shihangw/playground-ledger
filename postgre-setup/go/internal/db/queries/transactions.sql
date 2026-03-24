-- name: CreateTransaction :one
INSERT INTO transactions (
    transaction_type,
    status,
    source_account_id,
    destination_account_id,
    amount,
    currency,
    fee_amount,
    fee_account_id,
    idempotency_key,
    metadata
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: GetTransactionByID :one
SELECT * FROM transactions WHERE id = $1;

-- name: GetTransactionByIdempotencyKey :one
SELECT * FROM transactions WHERE idempotency_key = $1;

-- name: GetTransactionsByAccount :many
SELECT t.* FROM transactions t
WHERE t.source_account_id = $1 OR t.destination_account_id = $1
ORDER BY t.created_at DESC
LIMIT $2 OFFSET $3;

-- name: DepositAtomic :one
-- Hot path: credit account balance + append ledger entry in one atomic statement.
-- Does NOT write to transactions table — that's async logging, not blocking.
WITH credit AS (
  UPDATE accounts SET balance = balance + sqlc.arg(amount), updated_at = now()
  WHERE accounts.id = sqlc.arg(account_id)
  RETURNING accounts.id, accounts.balance, accounts.currency
), entry AS (
  INSERT INTO ledger_entries (account_id, entry_type, amount, balance_after, description, metadata)
  SELECT credit.id, 'CREDIT', sqlc.arg(amount), credit.balance, sqlc.arg(description), sqlc.arg(metadata)
  FROM credit
  RETURNING ledger_entries.created_at
)
SELECT credit.currency, entry.created_at FROM credit, entry;

-- name: WithdrawAtomic :one
-- Hot path: debit account balance + append ledger entry in one atomic statement.
-- Does NOT write to transactions table — that's async logging, not blocking.
WITH debit AS (
  UPDATE accounts SET balance = balance - sqlc.arg(amount), updated_at = now()
  WHERE accounts.id = sqlc.arg(account_id)
  RETURNING accounts.id, accounts.balance, accounts.currency
), entry AS (
  INSERT INTO ledger_entries (account_id, entry_type, amount, balance_after, description, metadata)
  SELECT debit.id, 'DEBIT', sqlc.arg(amount), debit.balance, sqlc.arg(description), sqlc.arg(metadata)
  FROM debit
  RETURNING ledger_entries.created_at
)
SELECT debit.currency, entry.created_at FROM debit, entry;

-- name: UpdateTransactionStatus :one
UPDATE transactions
SET status = $2
WHERE id = $1
RETURNING *;
