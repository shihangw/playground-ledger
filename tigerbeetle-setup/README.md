# tigerbeetle-setup

Verify a local TigerBeetle instance works and measure throughput before wiring up sidecars.

## Prerequisites

- Docker + Docker Compose
- Go 1.22+

## Quick start

```bash
# 1. Start TigerBeetle
docker compose up --build -d

# 2. Run all tests
go run .
```

## Starting TigerBeetle

```bash
docker compose up --build -d
```

The container formats a fresh data file on first start (`/data/0_0.tigerbeetle`) then listens on `localhost:3000`. Grid cache is allocated at **1024 MiB** (production mode — no `--development` flag).

To stop and wipe data:

```bash
docker compose down -v   # -v also removes the data volume
```

## Running the tests

```bash
go run .
```

### What each step tests

| Step | Scenario | What is asserted |
|------|----------|-----------------|
| 1 | **Basic transfer** | Transfer 500 units alice → bob; verify `debits_posted` and `credits_posted` |
| 2 | **Two-phase post** | Reserve 200 as pending; post it; assert pending clears and posted settles |
| 3 | **Two-phase void** | Reserve 150 as pending; void (cancel) it; assert no funds moved |
| 4 | **Waterfall draw** | Fund 3 source accounts (300/500/700); draw 1200 in priority order; assert A+B exhausted, C has 300 left |
| 5 | **Linked batch atomicity** | Link a valid transfer to an overdraft; assert both are rejected (`TransferLinkedEventFailed`) |
| 6 | **Balance constraint** | Attempt overdraft on `DebitsMustNotExceedCredits` account; assert `TransferExceedsCredits` |
| 7 | **Throughput** | Hammer concurrent batched transfers for `--duration`; report TPS vs 50k target |

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--address` | `127.0.0.1:3000` | TigerBeetle address |
| `--cluster` | `0` | Cluster ID |
| `--duration` | `10s` | Throughput test duration |
| `--concurrency` | `32` | Parallel goroutines for throughput test |
| `--batch` | `8189` | Transfers per batch (max 8189 in production mode) |

Skip to throughput only, with a longer run:

```bash
go run . --duration 30s
```

## Expected output

```
Connected to TigerBeetle at 127.0.0.1:3000 (cluster 0)

── Step 1: basic transfer ──────────────────────────────
  alice=…<id>  bob=…<id>
  account   debits_posted  credits_posted
  …<id>     500            0
  …<id>     0              500
  ✓ balances correct

── Step 2: two-phase post ──────────────────────────────
  pending: 200 reserved
  ✓ post: pending cleared, posted=200

── Step 3: two-phase void ──────────────────────────────
  pending: 150 reserved
  ✓ void: reservation cancelled, no funds moved

── Step 4: waterfall draw ──────────────────────────────
  sources funded: A=300  B=500  C=700
  waterfall draw: target=1200
    draw 300 from …<srcA>
    draw 500 from …<srcB>
    draw 400 from …<srcC>
  ✓ drew 1200: A exhausted, B exhausted, C has 300 remaining

── Step 5: linked batch atomicity ──────────────────────
  goodSrc funded with 100
  transfer[0] result: TransferLinkedEventFailed (linked rollback)
  transfer[1] result: TransferExceedsCredits (overdraft)
  ✓ linked rollback: goodSrc intact, dest received nothing

── Step 6: balance constraint ──────────────────────────
  strict account: DebitsMustNotExceedCredits=true, balance=0
  overdraft result: TransferExceedsCredits
  ✓ overdraft correctly rejected

── Step 7: throughput (10s, 32 goroutines, batch=8189) ────
  pre-created 64 accounts

  Duration:     12.9s
  Transfers:    1261106
  Errors:       0
  Observed TPS: 97853
  ✓ target of 50000 TPS met
```

### Throughput notes

**32 goroutines** is the sweet spot on this setup — beyond that, extra concurrency adds coordination overhead without increasing throughput.

| Mode | Max batch | Observed TPS (GCP VM) |
|------|-----------|----------------------|
| `--development` | 253 | ~40k |
| production (default) | 8189 | ~100k |

On dedicated bare-metal hardware TigerBeetle sustains 1M+ TPS.

## Benchmark: TigerBeetle + Cloud SQL (PostgreSQL)

### Architecture

```
Application
    │
    ├─► Cloud SQL PostgreSQL  — account metadata (user → TB account IDs, priority)
    │       user_accounts(user_id, account_id, priority)
    │
    └─► TigerBeetle           — financial ledger (all balances, every transfer)
```

Each benchmark operation runs in two variants back-to-back:

| Variant | What it tests |
|---|---|
| **TB only** | Account IDs hardcoded at setup — pure TigerBeetle throughput, no metadata cost |
| **PG → TB** | Account IDs fetched from Cloud SQL at runtime — realistic production path |

The delta between the two variants is the **cost of the metadata round-trip**.

### Infrastructure

| Component | Detail |
|---|---|
| TigerBeetle | Single-node Docker container, same VM |
| VM | GCP `e2-standard-4` (4 vCPU / 16 GB), `us-central1-a` |
| Cloud SQL | `db-custom-2-8192` ENTERPRISE, Postgres 16, `us-central1`, private IP `10.46.1.3` |
| Network (PG) | Same-region VPC private IP, ~0.3 ms base RTT |
| Concurrency | 32 goroutines, 15 s per scenario |

---

### Scenario 1 — Waterfall withdrawal

**What it models:** A user has 4 accounts ranked by priority (e.g. checking → savings → credit → wallet). A withdrawal drains them in order atomically — if any leg fails, the whole draw rolls back.

**Contention:** None. Each goroutine operates on its own independent set of 4 accounts. This isolates per-operation overhead with no lock or event-loop queuing.

**TB operation:** `CreateTransfers` — **8 waterfall chains per TB call** (default `--waterfall-batch 8`). Each chain is a 7-transfer linked sequence implementing the TB [Balancing Debits recipe](https://docs.tigerbeetle.com/coding/recipes/multi-debit-credit-transfers/): SETUP→LIMIT establishes the withdrawal ceiling, four `BalancingDebit`+`BalancingCredit` source transfers drain accounts in priority order without pre-reading balances, SETUP→X delivers the collected amount, and LIMIT→SETUP (not linked) clears the ceiling. Batching 8 chains into one `CreateTransfers` call amortises the fixed per-call overhead (network round-trip + TB event-loop wakeup) across 8 transactions.

**PG query (one round-trip for all 8 chains):**
```sql
SELECT user_id, account_id FROM user_accounts
WHERE user_id = ANY($1)   -- 8 user IDs
ORDER BY user_id, priority
```

| Variant | Calls | Txns/s | p50/call | p99/call | p999/call | Errors |
|---|---|---|---|---|---|---|
| **TB only** | 13 858 | 7 251 | 14.5 ms | 327.4 ms | 417.1 ms | 0 |
| **PG → TB** | 13 176 | 7 000 | 14.5 ms | 363.4 ms | 668.7 ms | 0 |

> p50/p99 are per TB call (= 8 chains). Per-transaction p50 ≈ **1.8 ms**.

**PG metadata overhead: −3% Txns/s, 0 ms p50/call.** Batching the PG lookup into one `ANY($8)` query cuts metadata cost to ~0.25 ms per transaction — negligible vs the TB round-trip. The `ANY()` query also naturally staggers goroutine arrivals at TB, mirroring the contention-smoothing seen in Scenario 2.

**Effect of batching (`--waterfall-batch`):**

| Batch | Txns/s | per-txn p50 | p99/call |
|---|---|---|---|
| 1 (unbatched) | 2 788 | 7.1 ms | 85 ms |
| **8 (default)** | **7 251** | **~1.8 ms** | 327 ms |
| 16 | 8 019 | ~1.3 ms | 520 ms |

Batch=8 gives a **+160% throughput gain** over unbatched. Batch=16 adds only another +10% while doubling p99 — diminishing returns because the TB event loop processes larger batches with proportionally longer latency.

---

### Scenario 2 — Hot account withdrawal

**What it models:** A shared pool, float account, or hot wallet that many concurrent users withdraw from simultaneously. The single source account is the contention point.

**Contention:** All 32 goroutines debit the **same TB source account** in every iteration. This is intentional — it directly tests how each system handles write contention on a single balance. Each goroutine has its own destination account to isolate the credit side.

**TB operation:** `CreateTransfers` — single transfer per op.

**PG query:**
```sql
SELECT account_id FROM user_accounts
WHERE user_id = $1
ORDER BY priority LIMIT 1
```

| Variant | Ops | Txns/s | p50 | p99 | p999 | Errors |
|---|---|---|---|---|---|---|
| **TB only** | 60 519 | 4 031 | 4.4 ms | 87.6 ms | 487.8 ms | 0 |
| **PG → TB** | 62 651 | 4 176 | 5.9 ms | 20.4 ms | 208.0 ms | 0 |

**PG metadata overhead: effectively none (+4% Txns/s, +1.5 ms p50).** The notable result is that p99 is *better* with PG→TB (20 ms vs 88 ms). The PG query naturally staggers goroutine arrivals at TB — each goroutine spends ~1.5 ms in PG before hitting TB — which reduces simultaneous contention on the hot account and smooths out the p99 spike.

TigerBeetle sustains ~4 000 ops/s under 32-goroutine contention on a single account with no errors. A PostgreSQL ledger under the same load would serialize all 32 goroutines behind a row lock, collapsing throughput.

---

### Scenario 3 — Fan-out to 1 000 accounts

**What it models:** One payer distributes funds to 1 000 recipients in a single operation — payroll, airdrop, batch settlement. Each worker has its own independent source and destination set.

**Contention:** None between workers. Within a single operation, 1 000 destination accounts are credited — TB processes these as one batch in its event loop.

**TB operation:** `CreateTransfers` — unlinked batch of 1 000 transfers (individual transfers are independent; atomicity is not required for fan-out).

**PG queries:**
```sql
-- Payer account
SELECT account_id FROM user_accounts WHERE user_id = $1 LIMIT 1

-- All payee accounts in one round-trip
SELECT account_id FROM user_accounts WHERE user_id = ANY($1)
```

| Variant | Ops | Txns/s | p50 | p99 | p999 | Errors |
|---|---|---|---|---|---|---|
| **TB only** | 1 633 | 106 872 | 213.5 ms | 676.2 ms | 712.3 ms | 0 |
| **PG → TB** | 1 219 | 78 735 | 320.2 ms | 1 334.8 ms | 1 371.5 ms | 0 |

**PG metadata overhead: −26% Txns/s, +107 ms p50.** The `ANY($1000)` query scanning 1 000 user IDs adds ~100 ms per operation — the largest relative cost of any scenario. p99 diverges strongly between variants (676 ms vs 1 335 ms): the PG lookup's ~100 ms cost, multiplied by a 32-goroutine concurrency, creates queuing that TB's batch commit hides in the TB-only case.

---

### Results summary

> **Txns/s** = complete business transactions per second (1 waterfall = 1 txn; 1 fan-out to 1 000 = 1 000 txns).

| Scenario | Variant | Txns/s | p50/txn | p99/call |
|---|---|---|---|---|
| 1. Waterfall (batch=8) | TB only | 7 251 | ~1.8 ms | 327 ms |
| 1. Waterfall (batch=8) | PG → TB | 7 000 | ~1.8 ms | 363 ms |
| 2. Hot withdrawal | TB only | 4 031 | 4.4 ms | 88 ms |
| 2. Hot withdrawal | PG → TB | 4 176 | 5.9 ms | 20 ms |
| 3. Fan-out→1000 | TB only | 106 872 | 0.21 ms | 676 ms |
| 3. Fan-out→1000 | PG → TB | 78 735 | 0.27 ms | 1 335 ms |

**Cloud SQL metadata overhead per operation:**

| Scenario | PG query | Latency added (p50/txn) | Throughput cost |
|---|---|---|---|
| Waterfall | `SELECT` 32 rows, `ANY($8)` | ~0 ms (amortised) | −3% |
| Hot withdrawal | `SELECT` 1 row, index scan | +1.5 ms | 0% (staging effect) |
| Fan-out | `SELECT` 1 000 rows, `ANY($1000)` | +107 ms | −26% |

Single-row lookups cost ~2 ms; batching them into `ANY()` queries amortises this to near zero. The `ANY($1000)` fan-out is the expensive outlier — cache payee IDs in the application layer if that matters.

### Re-running

```bash
go run ./bench \
  --tb-address 127.0.0.1:3000 \
  --pg-dsn "postgres://postgres:PASSWORD@10.46.1.3/ledger_bench" \
  --duration 15s \
  --concurrency 32 \
  --fanout-dests 1000 \
  --waterfall-batch 8
```

Cloud SQL instance: `ledger-bench-pg` (`us-central1`, private IP `10.46.1.3`).

---

## Next steps

Once throughput is confirmed, the full pipeline adds:
- **RabbitMQ** — AMQP broker (CDC target for `tigerbeetle amqp`)
- **Kafka + Kafka Connect** — bridges RabbitMQ → Kafka for durable high-throughput streaming
- **ClickHouse** — consumes from Kafka, stores events for OLAP queries
- **Backup sidecar** — periodic snapshots of the TigerBeetle data file
