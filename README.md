# Playground Ledger

A scalable, high-performance ledger-based billing system built with Go and CockroachDB, with a React admin dashboard.

## Prerequisites

- Go 1.21+
- Node.js 18+
- GCP project with Secret Manager enabled
- CockroachDB cluster (we use CockroachDB Serverless)
- Service account with `secretmanager.secretAccessor` role

## Project Structure

```
├── go/                           # Go backend
│   ├── cmd/
│   │   ├── api/main.go           # API server
│   │   └── migrate/main.go       # Database migrations
│   ├── internal/
│   │   ├── api/                  # HTTP handlers & middleware
│   │   ├── config/               # Configuration & secrets
│   │   ├── db/
│   │   │   ├── migrations/       # SQL migrations
│   │   │   ├── queries/          # sqlc SQL queries
│   │   │   └── generated/        # sqlc generated code
│   │   ├── ledger/               # Core ledger operations
│   │   └── wallet/               # User-facing wallet service
│   ├── sqlc.yaml                 # sqlc configuration
│   └── go.mod
└── ts/
    └── app/
        └── site/                 # React admin dashboard (Vite)
```

## Setup

### Backend (Go)

#### 1. Install dependencies

```bash
cd go
go mod download
```

#### 2. Install CLI tools

```bash
go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
go install github.com/pressly/goose/v3/cmd/goose@latest
```

#### 3. Configure secrets in GCP Secret Manager

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

#### 4. Set environment variable

```bash
export PLAYGROUND_GOOGLE_APPLICATION_CREDENTIALS="/path/to/service-account.json"
```

#### 5. Run migrations

```bash
cd go
go run cmd/migrate/main.go -cmd up
```

#### 6. Start the API server

```bash
cd go
go run cmd/api/main.go
```

Server starts on `http://localhost:8080`

### Frontend (React Dashboard)

#### 1. Install dependencies

```bash
cd ts/app/site
npm install
```

#### 2. Start development server

```bash
npm run dev
```

Dashboard starts on `http://localhost:3000`

#### 3. Build for production

```bash
npm run build
```

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

The admin dashboard has a token input in the top-right corner for switching users.

### Production
Use WorkOS session tokens.

## Idempotency

All mutating operations (POST) require an `Idempotency-Key` header with a UUID:
```
Idempotency-Key: 550e8400-e29b-41d4-a716-446655440000
```

Repeating a request with the same key returns the original response without re-executing.

## Development Commands

### Backend

```bash
cd go

# Regenerate sqlc code
sqlc generate

# Apply migrations
go run cmd/migrate/main.go -cmd up

# Rollback last migration
go run cmd/migrate/main.go -cmd down

# Check migration status
go run cmd/migrate/main.go -cmd status
```

### Frontend

```bash
cd ts/app/site

# Development server
npm run dev

# Build
npm run build

# Type check
npm run typecheck
```

## Admin Dashboard Features

- **Dashboard**: Overview of accounts and balances
- **Accounts**: View all accounts, deposit/withdraw funds
- **Transactions**: View transaction history per account
