-- name: CreateAccount :one
INSERT INTO accounts (user_id, currency)
VALUES ($1, $2)
RETURNING *;

-- name: GetAccountByID :one
SELECT * FROM accounts WHERE id = $1;

-- name: GetAccountForUpdate :one
SELECT * FROM accounts WHERE id = $1 FOR UPDATE;

-- name: GetAccountByUserAndCurrency :one
SELECT * FROM accounts WHERE user_id = $1 AND currency = $2;

-- name: GetAccountByUserAndCurrencyForUpdate :one
SELECT * FROM accounts WHERE user_id = $1 AND currency = $2 FOR UPDATE;

-- name: GetAccountsByUser :many
SELECT * FROM accounts WHERE user_id = $1 ORDER BY currency;

-- name: UpdateAccountBalance :one
UPDATE accounts
SET balance = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DebitAccount :one
UPDATE accounts
SET balance = balance - $2, updated_at = now()
WHERE id = $1 AND balance >= $2
RETURNING *;

-- name: CreditAccount :one
UPDATE accounts
SET balance = balance + $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: GetOrCreateAccount :one
INSERT INTO accounts (user_id, currency)
VALUES ($1, $2)
ON CONFLICT (user_id, currency) DO UPDATE SET updated_at = now()
RETURNING *;
