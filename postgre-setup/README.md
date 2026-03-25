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
go/
├── cmd/
│   ├── api/          # HTTP API server (wallet endpoints)
│   ├── migrate/      # goose database migrations
│   ├── bench/        # Direct-to-DB benchmark (no HTTP overhead)
│   ├── stress/       # HTTP stress + seed + verify CLI
│   └── dbstatus/     # Connection health, migration status, row counts
└── internal/
    ├── api/          # HTTP handlers & middleware
    ├── config/       # Config loading (env vars + GCP Secret Manager)
    ├── db/
    │   ├── migrations/   # SQL migrations (goose)
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

### 7. Start the API server

```bash
cd go
go run cmd/api/main.go
```

Server starts on `http://localhost:8080`.

---

## Benchmark (TB-style)

Three scenarios modelled after the [TigerBeetle benchmark](https://docs.tigerbeetle.com). Runs direct-to-AlloyDB — no HTTP server involved.

**Hardware:** AlloyDB N2, 32 vCPU, 256 GB RAM
**Settings:** `synchronous_commit=off`, `lock_timeout=200ms`, concurrency=32, duration=15s per scenario

### Waterfall evolution

The waterfall scenario (S1) models the real priority order: daily credit → monthly credit → bonus credit → cash. Priority accounts hold $5, cash holds $10 000. Each priority account serves 500 draws ($0.01 each) before depleting; a top-up fires every 500 draws to simulate a credit reset.

| Iteration | Approach | Txns/s | p50 | p99 | p999 |
|---|---|---|---|---|---|
| Baseline | `BEGIN … COMMIT` wrapping all 4 accounts every draw | 11 705 | 2.16 ms | 10.90 ms | 22.20 ms |
| + `depleted_at` | Skip zero-balance accounts via partial index | 11 349 | 2.21 ms | 11.22 ms | 23.79 ms |
| + Optimistic CTE | Single statement for first account; full waterfall only on miss | **39 690** | **0.57 ms** | **4.83 ms** | **12.89 ms** |

**Optimistic CTE approach:** try the highest-priority account with one auto-committed CTE (`UPDATE … RETURNING` + `INSERT INTO ledger_entries` in the same statement). On success: no explicit `BEGIN`/`COMMIT` overhead. On miss (account depleted or empty): fall back to the standard `BEGIN … COMMIT` waterfall over the remaining accounts. This is the pattern used in production — daily credit covers the vast majority of draws.

### All scenarios (optimistic waterfall)

| Scenario | Description | Txns/s | p50 | p99 | p999 | Errors |
|---|---|---|---|---|---|---|
| 1 — Waterfall | 4-account groups (daily→monthly→bonus→cash), optimistic CTE | **39 690** | 0.57 ms | 4.83 ms | 12.89 ms | 0 |
| 2 — Hot account | All 32 goroutines debit the same account | 6 484 | 3.57 ms | 21.21 ms | 32.09 ms | 0 |
| 3 — Fan-out 1 000 | 1 job = 1 000 pipelined deposits | 78 867 | 408.91 ms | 499.23 ms | 529.21 ms | 0 |

**Key findings:**
- **Optimistic CTE eliminates explicit transaction overhead** for the common case. A single SQL statement is auto-wrapped by PG internally — 1 round-trip vs 4 (BEGIN + UPDATE + INSERT + COMMIT). 3.5× throughput, 4× better p50.
- **`depleted_at` partial index** lets the waterfall skip zero-balance accounts without a row lock attempt. Payoff grows over time as priority accounts drain.
- **Hot-account contention**: row-level locks fully serialise 32 goroutines regardless of transport. Contention is the ceiling, not compute.
- **Fan-out**: 78k txns/s via pgx pipeline protocol. TigerBeetle achieves ~97k — event-loop batching is ~20% more efficient.
- Zero errors across all scenarios — `WHERE balance >= $1 AND depleted_at IS NULL` prevents overdraft without constraint violations.

### Running the benchmark

```bash
cd go

# All 3 scenarios
go run ./cmd/bench/

# Options
#   --scenario    0=all  1=waterfall  2=hot-account  3=fanout  (default 0)
#   --concurrency goroutines per scenario                       (default 32)
#   --duration    per-scenario duration                         (default 15s)
```

---

## Authentication

### Development

Use `dev_` prefixed tokens for testing:

```
Authorization: Bearer dev_testuser123
```

### Production

Replace the `dev_` token check in `AuthMiddleware` with your real auth provider (e.g. JWT validation).

## Idempotency

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

### Upgrading the instance

```bash
# Scale up CPU (requires instance to be stopped first)
gcloud alloydb instances update primary \
  --cluster=playground-ledger \
  --region=us-central1 \
  --cpu-count=4 \
  --project=sw-playground-ledger
```

### Updating the AlloyDB DSN secret

```bash
echo -n "postgres://postgres:newpassword@10.46.0.2:5432/postgres?sslmode=require" | \
  gcloud secrets versions add ALLOYDB_DSN \
    --data-file=- \
    --project=sw-playground-ledger
```

Old secret versions are retained automatically. To see version history:

```bash
gcloud secrets versions list ALLOYDB_DSN --project=sw-playground-ledger
```

### Rotating the postgres password

```bash
# 1. Set a new password on the cluster
gcloud alloydb users set-password postgres \
  --cluster=playground-ledger \
  --region=us-central1 \
  --password=NEWPASSWORD \
  --project=sw-playground-ledger

# 2. Update the secret
echo -n "postgres://postgres:NEWPASSWORD@10.46.0.2:5432/postgres?sslmode=require" | \
  gcloud secrets versions add ALLOYDB_DSN \
    --data-file=- \
    --project=sw-playground-ledger
```

### Deleting the cluster (teardown)

```bash
gcloud alloydb instances delete primary \
  --cluster=playground-ledger \
  --region=us-central1 \
  --project=sw-playground-ledger

gcloud alloydb clusters delete playground-ledger \
  --region=us-central1 \
  --project=sw-playground-ledger
```
