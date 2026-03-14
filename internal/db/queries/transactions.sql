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

-- name: UpdateTransactionStatus :one
UPDATE transactions
SET status = $2
WHERE id = $1
RETURNING *;
