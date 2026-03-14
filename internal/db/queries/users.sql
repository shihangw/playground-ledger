-- name: CreateUser :one
INSERT INTO users (external_id, email)
VALUES ($1, $2)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByExternalID :one
SELECT * FROM users WHERE external_id = $1;

-- name: UpdateUser :one
UPDATE users
SET email = $2, updated_at = now()
WHERE id = $1
RETURNING *;
