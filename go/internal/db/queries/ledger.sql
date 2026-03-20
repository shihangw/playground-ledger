-- name: CreateLedgerEntry :one
INSERT INTO ledger_entries (
    account_id,
    entry_type,
    amount,
    balance_after,
    transaction_id,
    description,
    metadata,
    idempotency_key
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetLedgerEntryByID :one
SELECT * FROM ledger_entries WHERE id = $1;

-- name: GetLedgerEntriesByAccount :many
SELECT * FROM ledger_entries
WHERE account_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: GetLedgerEntriesByTransaction :many
SELECT * FROM ledger_entries
WHERE transaction_id = $1
ORDER BY created_at;

-- name: GetLatestLedgerEntry :one
SELECT * FROM ledger_entries
WHERE account_id = $1
ORDER BY created_at DESC
LIMIT 1;
