# tigerbeetle-setup

Benchmarks and correctness tests for TigerBeetle, with PostgreSQL as a metadata store.

## Benchmark: TigerBeetle + PostgreSQL (local container)

> **Txns/s** = complete business transactions per second (1 waterfall draw = 1 txn; 1 fan-out to 1 000 recipients = 1 000 txns). All latency columns are **per transaction**.

### Results summary

| Scenario | Variant | Txns/s | p50 | p99 | max |
|---|---|---|---|---|---|
| 1. Waterfall (batch=8) | TB only | 20 064 | 382 µs | 20.2 ms | 41.5 ms |
| 1. Waterfall (batch=8) | PG → TB | 13 731 | 422 µs | 24.9 ms | 100.0 ms |
| 1. Waterfall | Optimistic | 11 347 | 660 µs | 53.5 ms | 340.9 ms |
| 1. Waterfall | Opt PG→TB | 5 032 | 2.3 ms | 95.9 ms | 603.0 ms |
| 2. Single Account Debit | TB only | 16 174 | 637 µs | 23.2 ms | 417.9 ms |
| 2. Single Account Debit | PG → TB | 12 150 | 1.2 ms | 35.9 ms | 372.6 ms |
| 3. Fan-out (1→1000) | TB only | 206 234 | 136 µs | 698 µs | 719 µs |
| 3. Fan-out (1→1000) | PG → TB | 161 926 | 183 µs | 670 µs | 718 µs |

**Local Postgres container overhead:**

| Scenario | PG query | p50 delta | Throughput delta |
|---|---|---|---|
| Waterfall (batch=8) | `SELECT` 32 rows, `ANY($8)` | +40 µs | −32% |
| Waterfall (Optimistic) | `SELECT` 4 rows, `ORDER BY priority` | +1.6 ms | −56% |
| Single Account Debit | `SELECT` 1 row, index scan | +0.6 ms | −25% |
| Fan-out | `SELECT` 1 000 rows, `ANY($1000)` | +47 µs/txn | −21% |

On NVMe the TB event loop is substantially faster than on `pd-standard`, so even local Postgres container latency (~0.1 ms RTT) becomes a larger fraction of total op time — the overhead percentage is higher than the Cloud SQL numbers despite lower absolute RTT.

---

### Scenario 1 — Waterfall withdrawal

| Variant | Txns/s | p50 | p99 | max |
|---|---|---|---|---|
| TB only (batch=8) | 20 064 | 382 µs | 20.2 ms | 41.5 ms |
| PG → TB (batch=8) | 13 731 | 422 µs | 24.9 ms | 100.0 ms |
| Optimistic | 11 347 | 660 µs | 53.5 ms | 340.9 ms |
| Opt PG→TB | 5 032 | 2.3 ms | 95.9 ms | 603.0 ms |

**Why batch=8 beats Optimistic on throughput:** batch=8 amortises one TB round-trip across 8 txns (→ 382 µs p50). The Optimistic path pays one round-trip per txn (→ 660 µs p50). The wide Optimistic p99 (53.5 ms) reflects the full 7-transfer fallback chain firing when A cascades to B or C. For a single end-user transaction in isolation, Optimistic is the lower-latency choice when A is funded.

**Effect of batching — full chain:**

| Batch | Txns/s | p50/txn |
|---|---|---|
| 1 (unbatched) | ~2 600 | ~5 ms |
| **8 (default)** | **20 064** | **382 µs** |

---

### Scenario 2 — Single Account Debit

| Variant | Txns/s | p50 | p99 | max |
|---|---|---|---|---|
| TB only | 16 174 | 637 µs | 23.2 ms | 417.9 ms |
| PG → TB | 12 150 | 1.2 ms | 35.9 ms | 372.6 ms |

TigerBeetle sustains ~16 000 ops/s under 32-goroutine contention on a single account with no errors. A PostgreSQL ledger under the same load would serialise all 32 goroutines behind a row lock.

---

### Scenario 3 — Fan-out (1 Source → 1 000 Recipients)

| Variant | Txns/s | p50/txn | p99/txn | max/txn |
|---|---|---|---|---|
| TB only | 206 234 | 136 µs | 698 µs | 719 µs |
| PG → TB | 161 926 | 183 µs | 670 µs | 718 µs |

Per-txn latency stays sub-millisecond in both variants — the 1 000-transfer batch amortises the TB round-trip to ~136 µs per recipient on NVMe. Cache payee IDs in the application layer to eliminate the −21% PG overhead.

---

### Re-running

```bash
go run ./bench \
  --tb-address 127.0.0.1:3000 \
  --pg-dsn "postgres://postgres:bench@172.18.0.7/ledger_bench" \
  --duration 15s \
  --concurrency 32 \
  --fanout-dests 1000 \
  --waterfall-batch 8
```

PostgreSQL: local Docker container (`bench-pg`) on the `ledger` bridge network.

---

### Benchmark setup

**Infrastructure:**

| Component | Detail |
|---|---|
| TigerBeetle | Single-node Docker container, same VM |
| VM | GCP `e2-standard-4` (4 vCPU / 16 GB), `us-central1-a` |
| Storage | 375 GB local NVMe SSD (`nvme0n1`), mounted at `~/ssd` |
| PostgreSQL | Docker container (`bench-pg`, Postgres 16), same VM, `ledger` bridge network |
| Network (PG) | Localhost bridge (~0.1 ms base RTT) |
| Concurrency | 32 goroutines, 15 s per scenario |

Each scenario runs two variants: **TB only** (account IDs hardcoded, no PG) and **PG → TB** (account IDs fetched from Postgres per op).

**Architecture:**

```
Application
    │
    ├─► PostgreSQL (local)  — account metadata (user → TB account IDs, priority)
    │       user_accounts(user_id, account_id, priority)
    │
    └─► TigerBeetle         — financial ledger (all balances, every transfer)
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

**Scenario 2 — Single Account Debit:** all 32 goroutines debit the same TB source account — intentional contention to test single-balance write throughput.

**Scenario 3 — Fan-out (1 Source → 1 000 Recipients):** one payer → 1 000 recipients in a single unlinked `CreateTransfers` batch per op.

---

## Throughput ceiling and hardware path

The benchmark above runs on a single-node Docker container backed by a **local NVMe SSD**. The observed ~20 k Txns/s (waterfall) and ~206 k Txns/s (fan-out) reflect the NVMe `fsync` latency floor, not a TigerBeetle design limit.

### Why `fsync` is the bottleneck

TigerBeetle commits every batch to disk before acknowledging clients. On this VM:

| Storage | `fsync` latency | Approx. TB throughput |
|---|---|---|
| `pd-standard` | ~1–2 ms | ~50–100 k transfers/s |
| `pd-ssd` | ~0.5 ms | ~200 k transfers/s |
| **Local NVMe SSD (this benchmark)** | **~50–100 µs** | **~200–500 k transfers/s** |
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

**Batching and the ceiling:** on NVMe the storage ceiling shifts to ~500 k transfers/s, so increasing `--waterfall-batch` beyond 8 has more room to help. The current 32-goroutine waterfall result (~20 k Txns/s × 7 transfers = ~140 k transfers/s) is well below the NVMe ceiling — the bottleneck is now the linked-chain serialisation cost, not storage.

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

| Mode | Max batch | Observed TPS (NVMe VM) |
|------|-----------|----------------------|
| `--development` | 253 | ~40k |
| production (default) | 8189 | ~278k |
