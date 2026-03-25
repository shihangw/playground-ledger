# tigerbeetle-setup

Benchmarks and correctness tests for TigerBeetle, with Cloud SQL as a metadata store.

## Benchmark: TigerBeetle + Cloud SQL (PostgreSQL)

> **Txns/s** = complete business transactions per second (1 waterfall draw = 1 txn; 1 fan-out to 1 000 recipients = 1 000 txns). All latency columns are **per transaction**.

### Results summary

| Scenario | Variant | Txns/s | p50 | p99 | max |
|---|---|---|---|---|---|
| 1. Waterfall (batch=8) | TB only | 7 654 | 1.9 ms | 29.8 ms | 52.5 ms |
| 1. Waterfall (batch=8) | PG → TB | 6 739 | 1.9 ms | 31.9 ms | 242.6 ms |
| 1. Waterfall | Optimistic | 3 649 | 3.7 ms | 122.4 ms | 915.0 ms |
| 1. Waterfall | Opt PG→TB | 2 127 | 7.9 ms | 165.3 ms | 1 168.6 ms |
| 2. Hot withdrawal | TB only | 5 753 | 3.6 ms | 31.2 ms | 206.8 ms |
| 2. Hot withdrawal | PG → TB | 4 701 | 5.2 ms | 49.4 ms | 144.2 ms |
| 3. Fan-out→1000 | TB only | 107 391 | 222 µs | 956 µs | 970 µs |
| 3. Fan-out→1000 | PG → TB | 99 852 | 297 µs | 872 µs | 896 µs |

**Cloud SQL metadata overhead:**

| Scenario | PG query | p50 delta | Throughput delta |
|---|---|---|---|
| Waterfall (batch=8) | `SELECT` 32 rows, `ANY($8)` | 0 ms | −12% |
| Waterfall (Optimistic) | `SELECT` 4 rows, `ORDER BY priority` | +4.2 ms | −42% |
| Hot withdrawal | `SELECT` 1 row, index scan | +1.6 ms | −18% |
| Fan-out | `SELECT` 1 000 rows, `ANY($1000)` | +75 µs/txn | −7% |

On the SSD the TB event loop is faster, so the PG RTT is a larger fraction of total op time. The staging benefit that appeared on `pd-standard` disappears — faster TB means goroutines spend proportionally less time queuing at the event loop, so staggering arrivals no longer helps.

---

### Scenario 1 — Waterfall withdrawal

| Variant | Txns/s | p50 | p99 | max |
|---|---|---|---|---|
| TB only (batch=8) | 7 654 | 1.9 ms | 29.8 ms | 52.5 ms |
| PG → TB (batch=8) | 6 739 | 1.9 ms | 31.9 ms | 242.6 ms |
| Optimistic | 3 649 | 3.7 ms | 122.4 ms | 915.0 ms |
| Opt PG→TB | 2 127 | 7.9 ms | 165.3 ms | 1 168.6 ms |

**Why batch=8 beats Optimistic on throughput:** batch=8 amortises one TB round-trip across 8 txns (→ 1.9 ms p50). The Optimistic path pays one round-trip per txn (→ 3.7 ms p50). The wide Optimistic p99 (122 ms) reflects the full 7-transfer fallback chain firing when A cascades to B or C. For a single end-user transaction in isolation, Optimistic is the lower-latency choice when A is funded.

**Effect of batching — full chain:**

| Batch | Txns/s | p50/txn |
|---|---|---|
| 1 (unbatched) | ~2 600 | ~5 ms |
| **8 (default)** | **7 654** | **1.9 ms** |

---

### Scenario 2 — Hot account withdrawal

| Variant | Txns/s | p50 | p99 | max |
|---|---|---|---|---|
| TB only | 5 753 | 3.6 ms | 31.2 ms | 206.8 ms |
| PG → TB | 4 701 | 5.2 ms | 49.4 ms | 144.2 ms |

TigerBeetle sustains ~5 700 ops/s under 32-goroutine contention on a single account with no errors. A PostgreSQL ledger under the same load would serialise all 32 goroutines behind a row lock.

---

### Scenario 3 — Fan-out to 1 000 accounts

| Variant | Txns/s | p50/txn | p99/txn | max/txn |
|---|---|---|---|---|
| TB only | 107 391 | 222 µs | 956 µs | 970 µs |
| PG → TB | 99 852 | 297 µs | 872 µs | 896 µs |

Per-txn latency stays sub-millisecond in both variants — the 1 000-transfer batch amortises the TB round-trip to ~220 µs per recipient. Cache payee IDs in the application layer to eliminate the −7% PG overhead.

---

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

### Benchmark setup

**Infrastructure:**

| Component | Detail |
|---|---|
| TigerBeetle | Single-node Docker container, same VM |
| VM | GCP `e2-standard-4` (4 vCPU / 16 GB), `us-central1-a` |
| Storage | 100 GB SSD persistent disk (`ssddisk-20260325-182823`), mounted at `/mnt/ssd` |
| Cloud SQL | `db-custom-2-8192` ENTERPRISE, Postgres 16, `us-central1`, private IP `10.46.1.3` |
| Network (PG) | Same-region VPC private IP, ~0.3 ms base RTT |
| Concurrency | 32 goroutines, 15 s per scenario |

Each scenario runs two variants: **TB only** (account IDs hardcoded, no PG) and **PG → TB** (account IDs fetched from Cloud SQL per op).

**Architecture:**

```
Application
    │
    ├─► Cloud SQL PostgreSQL  — account metadata (user → TB account IDs, priority)
    │       user_accounts(user_id, account_id, priority)
    │
    └─► TigerBeetle           — financial ledger (all balances, every transfer)
```

**Scenario 1 — Waterfall account setup (1 unit = $0.10):**

| Account | Balance | Role | Top-up |
|---|---|---|---|
| Daily credit (A) | 50 units ($5) | first priority | +50 every 150 draws |
| Monthly credit (B) | 50 units ($5) | fallback once A depleted | +50 every 150 draws |
| Bonus credit (C) | 50 units ($5) | fallback once A+B depleted | +50 every 150 draws |
| Cash (D) | 1 000 000 units ($100 000) | safety net | never depleted |

Each priority account serves exactly 50 draws before depleting. Top-up fires every 150 draws, so one cycle covers A (draws 1–50) → B (51–100) → C (101–150) → D (remainder), exercising all 4 accounts every cycle.

**TB recipe — full chain:** 7-transfer linked sequence ([Balancing Debits recipe](https://docs.tigerbeetle.com/coding/recipes/multi-debit-credit-transfers/)). SETUP→LIMIT establishes the withdrawal ceiling; four `BalancingDebit`+`BalancingCredit` transfers drain A→B→C→D in order without pre-reading balances; SETUP→X delivers the amount; LIMIT→SETUP (not linked) clears the ceiling. `--waterfall-batch 8` bundles 8 chains into one `CreateTransfers` call.

**TB recipe — Optimistic:** single direct debit from A (1 transfer). `DebitsMustNotExceedCredits` on A makes TB self-enforce the balance check. On `exceeds_credits`, falls back to the full 7-transfer chain using all 4 accounts.

**Scenario 2 — Hot account:** all 32 goroutines debit the same TB source account — intentional contention to test single-balance write throughput.

**Scenario 3 — Fan-out:** one payer → 1 000 recipients in a single unlinked `CreateTransfers` batch per op.

---

## Throughput ceiling and hardware path

The benchmark above runs on a single-node Docker container backed by a **GCP `pd-standard` persistent disk**. The observed ~7 000 Txns/s (waterfall) and ~100 k Txns/s (fan-out) ceilings are primarily an I/O artifact, not a TigerBeetle design limit.

### Why `fsync` is the bottleneck

TigerBeetle commits every batch to disk before acknowledging clients. On this VM:

| Storage | `fsync` latency | Approx. TB throughput |
|---|---|---|
| `pd-standard` (this benchmark) | ~1–2 ms | ~50–100 k transfers/s |
| `pd-ssd` | ~0.5 ms | ~200 k transfers/s |
| Local NVMe SSD | ~50–100 µs | ~500 k–1 M transfers/s |
| Bare-metal NVMe | ~20–50 µs | 1 M+ transfers/s (TB published figure) |

Each doubling of `fsync` speed roughly doubles TB throughput because the event loop spends the majority of its wall time waiting for the storage acknowledgement.

### Why local NVMe requires a cluster

Local SSDs are ephemeral — data is lost if the VM stops. The production solution is a **3-node TigerBeetle cluster** using VSR (Viewstamped Replication) consensus. Each node has its own local NVMe; the cluster tolerates one node failure without data loss. Durability comes from replication, not disk redundancy.

```
┌─────────────────────────────────────────────────────┐
│  3-node TB cluster (VSR consensus, quorum = 2/3)    │
│                                                     │
│  node-1 [local NVMe]  ──┐                           │
│  node-2 [local NVMe]  ──┼──► clients (any node)    │
│  node-3 [local NVMe]  ──┘                           │
└─────────────────────────────────────────────────────┘
```

**Latency trade-off:** the primary must wait for 2/3 nodes to `fsync` before committing (adds one cross-node RTT, ~0.5–1 ms intra-zone). Net result is still far faster than a single node on `pd-standard`.

**Batching and the ceiling:** increasing `--waterfall-batch` beyond 8 gives diminishing returns on this VM (~+15% from 8→128) because the batch of 32 concurrent goroutines already saturates the ~50 k transfers/s storage ceiling. On NVMe the ceiling shifts to ~500 k transfers/s, making larger batches worthwhile again.

---

## Next steps

Once throughput is confirmed, the full pipeline adds:
- **RabbitMQ** — AMQP broker (CDC target for `tigerbeetle amqp`)
- **Kafka + Kafka Connect** — bridges RabbitMQ → Kafka for durable high-throughput streaming
- **ClickHouse** — consumes from Kafka, stores events for OLAP queries
- **Backup sidecar** — periodic snapshots of the TigerBeetle data file

---

## Project setup

### Prerequisites

- Docker + Docker Compose
- Go 1.22+

### Starting TigerBeetle

```bash
docker compose up --build -d
```

The container formats a fresh data file on first start (`/data/0_0.tigerbeetle`) then listens on `localhost:3000`. Grid cache is allocated at **1024 MiB** (production mode — no `--development` flag).

To stop and wipe data:

```bash
docker compose down -v   # -v also removes the data volume
```

### Running the correctness tests

```bash
go run .
```

| Step | Scenario | What is asserted |
|------|----------|-----------------|
| 1 | **Basic transfer** | Transfer 500 units alice → bob; verify `debits_posted` and `credits_posted` |
| 2 | **Two-phase post** | Reserve 200 as pending; post it; assert pending clears and posted settles |
| 3 | **Two-phase void** | Reserve 150 as pending; void (cancel) it; assert no funds moved |
| 4 | **Waterfall draw** | Fund 3 source accounts (300/500/700); draw 1200 in priority order; assert A+B exhausted, C has 300 left |
| 5 | **Linked batch atomicity** | Link a valid transfer to an overdraft; assert both are rejected (`TransferLinkedEventFailed`) |
| 6 | **Balance constraint** | Attempt overdraft on `DebitsMustNotExceedCredits` account; assert `TransferExceedsCredits` |
| 7 | **Throughput** | Hammer concurrent batched transfers for `--duration`; report TPS vs 50k target |

Flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--address` | `127.0.0.1:3000` | TigerBeetle address |
| `--cluster` | `0` | Cluster ID |
| `--duration` | `10s` | Throughput test duration |
| `--concurrency` | `32` | Parallel goroutines for throughput test |
| `--batch` | `8189` | Transfers per batch (max 8189 in production mode) |

### Expected output

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

| Mode | Max batch | Observed TPS (GCP VM) |
|------|-----------|----------------------|
| `--development` | 253 | ~40k |
| production (default) | 8189 | ~100k |
