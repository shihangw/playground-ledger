# Playground Ledger

A scalable, high-performance ledger-based billing system built with Go and CockroachDB.

## Prerequisites

- Go 1.21+
- GCP project with Secret Manager enabled
- CockroachDB cluster (we use CockroachDB Serverless)
- Service account with `secretmanager.secretAccessor` role

## Setup

### 1. Install dependencies

```bash
go mod download
```

### 2. Install CLI tools

```bash
go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
go install github.com/pressly/goose/v3/cmd/goose@latest
```

### 3. Configure secrets in GCP Secret Manager

Create these secrets in your GCP project (`sw-playground-ledger`):

| Secret | Description |
|--------|-------------|
| `CRDB_DSN` | CockroachDB connection string |
| `WORKOS_API_KEY` | WorkOS API key for authentication |
| `WORKOS_CLIENT_ID` | WorkOS client ID |

Example CRDB_DSN format:
```
postgresql://user:password@host:26257/ledger?sslmode=verify-full
```

### 4. Set environment variable

```bash
export PLAYGROUND_GOOGLE_APPLICATION_CREDENTIALS="/path/to/service-account.json"
```

### 5. Run migrations

```bash
go run cmd/migrate/main.go -cmd up
```

## Running the Server

```bash
go run cmd/api/main.go
```

Server starts on `http://localhost:8080`

## API Endpoints

### Health Check
```bash
curl http://localhost:8080/health
```

### Get Accounts (creates user if not exists)
```bash
curl http://localhost:8080/v1/accounts \
  -H "Authorization: Bearer dev_myuser"
```

### Deposit
```bash
curl -X POST http://localhost:8080/v1/accounts/{account_id}/deposit \
  -H "Authorization: Bearer dev_myuser" \
  -H "Idempotency-Key: $(uuidgen)" \
  -H "Content-Type: application/json" \
  -d '{"amount": "100.00"}'
```

### Withdraw
```bash
curl -X POST http://localhost:8080/v1/accounts/{account_id}/withdraw \
  -H "Authorization: Bearer dev_myuser" \
  -H "Idempotency-Key: $(uuidgen)" \
  -H "Content-Type: application/json" \
  -d '{"amount": "25.00"}'
```

### Transfer
```bash
curl -X POST http://localhost:8080/v1/accounts/{account_id}/transfer \
  -H "Authorization: Bearer dev_myuser" \
  -H "Idempotency-Key: $(uuidgen)" \
  -H "Content-Type: application/json" \
  -d '{"to_account_id": "destination-uuid", "amount": "50.00"}'
```

### Get Transactions
```bash
curl http://localhost:8080/v1/accounts/{account_id}/transactions \
  -H "Authorization: Bearer dev_myuser"
```

## Authentication

### Development
Use `dev_` prefixed tokens for testing:
```
Authorization: Bearer dev_testuser123
```

### Production
Use WorkOS session tokens.

## Idempotency

All mutating operations (POST) require an `Idempotency-Key` header with a UUID:
```
Idempotency-Key: 550e8400-e29b-41d4-a716-446655440000
```

Repeating a request with the same key returns the original response without re-executing.

## Project Structure

```
├── cmd/
│   ├── api/main.go           # API server
│   └── migrate/main.go       # Database migrations
├── internal/
│   ├── api/                  # HTTP handlers & middleware
│   ├── config/               # Configuration & secrets
│   ├── db/
│   │   ├── migrations/       # SQL migrations
│   │   ├── queries/          # sqlc SQL queries
│   │   └── generated/        # sqlc generated code
│   ├── ledger/               # Core ledger operations
│   └── wallet/               # User-facing wallet service
├── sqlc.yaml                 # sqlc configuration
└── go.mod
```

## Development Commands

### Regenerate sqlc code
```bash
sqlc generate
```

### Migration commands
```bash
# Apply migrations
go run cmd/migrate/main.go -cmd up

# Rollback last migration
go run cmd/migrate/main.go -cmd down

# Check migration status
go run cmd/migrate/main.go -cmd status
```

## Example Test Flow

```bash
# 1. Start server
export PLAYGROUND_GOOGLE_APPLICATION_CREDENTIALS="/path/to/creds.json"
go run cmd/api/main.go &

# 2. Create user and get account
ACCOUNT=$(curl -s http://localhost:8080/v1/accounts \
  -H "Authorization: Bearer dev_alice" | jq -r '.[0].id')
echo "Account: $ACCOUNT"

# 3. Deposit $100
curl -X POST "http://localhost:8080/v1/accounts/${ACCOUNT}/deposit" \
  -H "Authorization: Bearer dev_alice" \
  -H "Idempotency-Key: $(uuidgen)" \
  -H "Content-Type: application/json" \
  -d '{"amount": "100.00"}'

# 4. Check balance
curl "http://localhost:8080/v1/accounts/${ACCOUNT}" \
  -H "Authorization: Bearer dev_alice"

# 5. Create second user
ACCOUNT2=$(curl -s http://localhost:8080/v1/accounts \
  -H "Authorization: Bearer dev_bob" | jq -r '.[0].id')

# 6. Transfer $30 from Alice to Bob
curl -X POST "http://localhost:8080/v1/accounts/${ACCOUNT}/transfer" \
  -H "Authorization: Bearer dev_alice" \
  -H "Idempotency-Key: $(uuidgen)" \
  -H "Content-Type: application/json" \
  -d "{\"to_account_id\": \"${ACCOUNT2}\", \"amount\": \"30.00\"}"

# 7. Check both balances
echo "Alice:" && curl -s "http://localhost:8080/v1/accounts/${ACCOUNT}" \
  -H "Authorization: Bearer dev_alice" | jq .balance
echo "Bob:" && curl -s "http://localhost:8080/v1/accounts/${ACCOUNT2}" \
  -H "Authorization: Bearer dev_bob" | jq .balance
```
