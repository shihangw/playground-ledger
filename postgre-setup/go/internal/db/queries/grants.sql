-- name: CreateGrant :one
INSERT INTO credit_grants (account_id, grant_type, initial_amount, remaining_amount, expires_at, idempotency_key, metadata, priority)
VALUES ($1, $2, $3, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetGrantByID :one
SELECT * FROM credit_grants WHERE id = $1;

-- name: GetGrantByIdempotencyKey :one
SELECT * FROM credit_grants WHERE idempotency_key = $1;

-- name: GetActiveGrantsByAccount :many
SELECT * FROM credit_grants
WHERE account_id = $1 AND status = 'ACTIVE'
ORDER BY priority ASC, expires_at ASC;

-- name: GetGrantsByAccount :many
SELECT * FROM credit_grants
WHERE account_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: GetGrantForUpdate :one
SELECT * FROM credit_grants WHERE id = $1 FOR UPDATE;

-- name: DrawdownGrant :one
UPDATE credit_grants
SET remaining_amount = remaining_amount - $2
WHERE id = $1 AND remaining_amount >= $2 AND status = 'ACTIVE'
RETURNING *;

-- name: DepleteGrant :exec
UPDATE credit_grants
SET remaining_amount = 0, status = 'DEPLETED'
WHERE id = $1;

-- name: ExpireActiveGrants :execrows
UPDATE credit_grants
SET status = 'EXPIRED'
WHERE expires_at < now() AND status = 'ACTIVE';

-- name: GetGrantBalance :one
SELECT COALESCE(SUM(remaining_amount), 0)::DECIMAL(28,12) as available_balance
FROM credit_grants
WHERE account_id = $1 AND status = 'ACTIVE';

-- name: CreateGrantLedgerEntry :one
INSERT INTO grant_ledger_entries (grant_id, entry_type, amount, remaining_after, description, idempotency_key)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetGrantLedgerEntries :many
SELECT * FROM grant_ledger_entries
WHERE grant_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;
