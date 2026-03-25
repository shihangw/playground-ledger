# PostgreSQL / AlloyDB Ledger

PostgreSQL implementation of a credit ledger for billing. Explores how far a standard relational DB can be pushed for high-throughput debit/credit workloads before requiring a specialised system (e.g. TigerBeetle).

The core question: **can AlloyDB handle a waterfall credit draw (daily → monthly → bonus → cash) with acceptable latency and throughput at scale?**

Answer: yes — 39 690 txns/s at p50 0.57 ms with the optimistic CTE pattern (see benchmark below).

## Prerequisites

- Go 1.21+
- GCP project with Secret Manager enabled
- AlloyDB cluster
- Service account with `secretmanager.secretAccessor` role

## Project Structure

```
scripts/
└── setup-alloydb.sh  # One-shot AlloyDB cluster/instance provisioning + secret setup
go/
├── Dockerfile        # Multi-stage build → alpine image (port 8080)
├── cmd/
│   ├── api/          # HTTP API server (wallet endpoints)
│   ├── migrate/      # goose database migrations
│   ├── bench/        # Direct-to-DB benchmark (no HTTP overhead)
│   ├── stress/       # HTTP stress + seed + verify CLI
│   └── dbstatus/     # Connection health, migration status, row counts
└── internal/
    ├── api/
    │   ├── handlers/ # wallet, grants, admin, stress HTTP handlers
    │   ├── middleware/ # auth, idempotency, CORS middleware
    │   └── router.go
    ├── config/       # Config loading (env vars + GCP Secret Manager)
    ├── db/
    │   ├── migrations/   # SQL migrations (goose) — 001–006
    │   ├── queries/      # sqlc SQL queries
    │   └── generated/    # sqlc generated Go code
    ├── ledger/       # Core ledger operations (deposit, withdraw, batch)
    ├── wallet/       # User-facing wallet service
    └── grants/       # Credit grant management
```

## Backend Setup

### 1. Install dependencies

```bash
cd go
go mod download
```

### 2. Install CLI tools

```bash
go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
go install github.com/pressly/goose/v3/cmd/goose@latest
```

### 3. Configure secrets in GCP Secret Manager

Create this secret in your GCP project (`sw-playground-ledger`):

| Secret | Description |
|--------|-------------|
| `ALLOYDB_DSN` | AlloyDB connection string |

Example DSN format:
```
# AlloyDB (private IP, SSL required)
postgres://postgres:password@10.46.0.2:5432/postgres?sslmode=require
```

To update a secret value:
```bash
echo -n "postgres://..." | gcloud secrets versions add ALLOYDB_DSN --data-file=-
```

### 4. Set environment variable

```bash
export PLAYGROUND_GOOGLE_APPLICATION_CREDENTIALS="/path/to/service-account.json"
```

### 5. Set DSN (development)

In development you can set the DSN directly without Secret Manager:
```bash
export ALLOYDB_DSN="postgres://postgres:password@10.46.0.2:5432/postgres?sslmode=require"
```

### 6. Run migrations

```bash
cd go
go run cmd/migrate/main.go -cmd up
```

### 7. Start the API server (optional)

```bash
cd go
go run cmd/api/main.go
```

Server starts on `http://localhost:8080`. Not needed for benchmarks or migrations — only required if you want the HTTP API.

---

## Benchmark (TB-style)

Three scenarios modelled after the [TigerBeetle benchmark](https://docs.tigerbeetle.com). Runs direct-to-AlloyDB — no HTTP server involved.

**Hardware:** AlloyDB N2, 32 vCPU, 256 GB RAM
**Settings:** `synchronous_commit=off`, `lock_timeout=200ms`, concurrency=32, duration=15s per scenario

### Results

| Scenario | Description | Txns/s | p99 | max | Errors |
|---|---|---|---|---|---|---|
| 1 — Waterfall | Realistic cascade: A→B→C→D, full cycle every 150 draws | **11 434** | 13.03 ms | 93.85 ms | 0 |
| 2 — Hot account | All 32 goroutines debit the same account | 6 484 | 21.21 ms | 32.09 ms | 0 |
| 3 — Fan-out 1 000 | 1 job = 1 000 pipelined deposits; latency is per-batch | 78 867 | 499.23 ms | 529.21 ms | 0 |

**Key findings:**
- **Waterfall realistic cost**: 2/3 of draws cascade through B and C, hitting the full `BEGIN…COMMIT` path. p99 widens to 13 ms; max spikes to ~94 ms when a top-up write coincides with a deep cascade.
- **Optimistic CTE** (when A is always funded): 39 690 txns/s, p99 4.83 ms — 3.5× faster. Single auto-committed CTE, no explicit `BEGIN`/`COMMIT`.
- **`depleted_at` partial index** skips zero-balance accounts without a row lock attempt.
- **Hot-account contention**: row-level locks fully serialise 32 goroutines. Contention is the ceiling, not compute.
- **Fan-out**: 78k txns/s via pgx pipeline protocol. TigerBeetle achieves ~97k — event-loop batching is ~20% more efficient.
- Zero errors across all scenarios — `WHERE balance >= $1 AND depleted_at IS NULL` prevents overdraft without constraint violations.

### Waterfall evolution

| Iteration | Setup | Txns/s | p99 | max |
|---|---|---|---|---|
| Baseline | `BEGIN…COMMIT` wrapping all 4 accounts every draw | 11 705 | 10.90 ms | 22.20 ms |
| + `depleted_at` | Skip zero-balance accounts via partial index | 11 349 | 11.22 ms | 23.79 ms |
| + Optimistic CTE | Single CTE for A; `BEGIN…COMMIT` only on miss (accounts rarely deplete at $0.01/draw) | **39 690** | **4.83 ms** | **12.89 ms** |
| Realistic cascade | $0.10/draw — A depletes at 50 draws, full A→B→C cycle every 150 draws | 11 434 | 13.03 ms | 93.85 ms |

### Waterfall account setup

1 unit = $0.10. Each goroutine owns an exclusive 4-account group.

| Account | Balance | Role | Top-up |
|---|---|---|---|
| Daily credit (A) | 50 units ($5) | first priority | +50 units every 150 draws |
| Monthly credit (B) | 50 units ($5) | fallback once A depleted | +50 units every 150 draws |
| Bonus credit (C) | 50 units ($5) | fallback once A+B depleted | +50 units every 150 draws |
| Cash (D) | 1 000 000 units ($100 000) | safety net | never topped up |

Each priority account depletes after exactly 50 draws. Top-up fires every 150 draws (= 3 × 50), resetting A, B, C back to $5. One cycle: A (draws 1–50) → B (51–100) → C (101–150) → D (remainder), then reset.

## Running the benchmark

```bash
cd go

# All 3 scenarios
go run ./cmd/bench/

# Options
#   --scenario    0=all  1=waterfall  2=hot-account  3=fanout  (default 0)
#   --concurrency goroutines per scenario                       (default 32)
#   --duration    per-scenario duration                         (default 15s)
```

No HTTP server needed — connects directly to AlloyDB. Requires `ALLOYDB_DSN` to be set.

---

## API Server

### Authentication

Use `dev_` prefixed tokens for testing:

```
Authorization: Bearer dev_testuser123
```

Replace the `dev_` token check in `AuthMiddleware` with your real auth provider (e.g. JWT validation) for production.

### Idempotency

All mutating operations (POST) require an `Idempotency-Key` header with a UUID:

```
Idempotency-Key: 550e8400-e29b-41d4-a716-446655440000
```

Repeating a request with the same key returns the original response without re-executing.

## Development Commands

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

# Check database health, version, migrations, and row counts
go run cmd/dbstatus/main.go
```

---

## AlloyDB Setup (GCP)

The AlloyDB cluster (`playground-ledger`, `us-central1`) is provisioned in the same VPC as the GCE VM and connects directly via private IP.

Export the playground project before running any commands:

```bash
export LEDGER_GCP_PROJECT=sw-playground-ledger
```

The setup script and all `gcloud` snippets below read from `LEDGER_GCP_PROJECT`.

### Automated provisioning

`scripts/setup-alloydb.sh` handles the full first-time setup in one command: enables required GCP APIs, generates and stores a random postgres password in Secret Manager, creates the cluster and primary instance (if they don't exist), saves the `ALLOYDB_DSN` secret, and runs migrations.

```bash
# LEDGER_GCP_PROJECT must be exported before running
./scripts/setup-alloydb.sh

# Override any defaults
./scripts/setup-alloydb.sh --cpu-count 4 --project my-project --region us-east1 --network my-vpc
```

Subsequent runs are idempotent — existing resources and secrets are left unchanged.

### Docker

The API server ships as a minimal alpine image:

```bash
cd go

# Build
docker build -t ledger-api .

# Run (pass DSN directly for local testing)
docker run -p 8080:8080 \
  -e ALLOYDB_DSN="postgres://postgres:password@10.46.0.2:5432/postgres?sslmode=require" \
  ledger-api
```

### Upgrading the instance

```bash
# Scale up CPU (requires instance to be stopped first)
gcloud alloydb instances update primary \
  --cluster=playground-ledger \
  --region=us-central1 \
  --cpu-count=4 \
  --project=$LEDGER_GCP_PROJECT
```

### Updating the AlloyDB DSN secret

```bash
echo -n "postgres://postgres:newpassword@10.46.0.2:5432/postgres?sslmode=require" | \
  gcloud secrets versions add ALLOYDB_DSN \
    --data-file=- \
    --project=$LEDGER_GCP_PROJECT
```

Old secret versions are retained automatically. To see version history:

```bash
gcloud secrets versions list ALLOYDB_DSN --project=$LEDGER_GCP_PROJECT
```

### Rotating the postgres password

```bash
# 1. Set a new password on the cluster
gcloud alloydb users set-password postgres \
  --cluster=playground-ledger \
  --region=us-central1 \
  --password=NEWPASSWORD \
  --project=$LEDGER_GCP_PROJECT

# 2. Update the secret
echo -n "postgres://postgres:NEWPASSWORD@10.46.0.2:5432/postgres?sslmode=require" | \
  gcloud secrets versions add ALLOYDB_DSN \
    --data-file=- \
    --project=$LEDGER_GCP_PROJECT
```

### Deleting the cluster (teardown)

```bash
gcloud alloydb instances delete primary \
  --cluster=playground-ledger \
  --region=us-central1 \
  --project=$LEDGER_GCP_PROJECT

gcloud alloydb clusters delete playground-ledger \
  --region=us-central1 \
  --project=$LEDGER_GCP_PROJECT
```
