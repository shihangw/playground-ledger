# Playground Ledger — Research Summary

Learnings from benchmarking and designing an in-house billing ledger to replace Orb. Covers
AlloyDB (PostgreSQL), TigerBeetle, industry reference, and architecture trade-offs.

---

## Context

Migrating freemium users off Orb to an in-house billing engine. Phase 1 goals:

- Ledger + credit grants for freemium users (top priority)
- Independent revenue verification against Orb
- Shadow billing for paying customers (run in parallel, compare invoices)
- Orb remains authoritative for paying customers until shadow mode proves parity

**Stack decision: AlloyDB (PostgreSQL-compatible) for v1.** Not up for debate — 18x headroom
at 10x projected volume (~5,500 TPS), team has Postgres expertise, and the timeline (8 weeks)
does not allow learning a new database.

---

## Benchmark Results

### AlloyDB

> AlloyDB N2, 32 vCPU, 256 GB RAM, `us-central1`, private IP (same-region VPC).
> `lock_timeout=200ms`, concurrency=32, duration=15s.

#### `synchronous_commit=off` (ms data loss window)


| Scenario                      | Description                                       | Txns/s     | p99     | max     |
| ----------------------------- | ------------------------------------------------- | ---------- | ------- | ------- |
| 1. Waterfall                  | Full A→B→C→D cascade, depletes every 50 draws     | 11 434     | 13.0 ms | 93.9 ms |
| 1. Waterfall (optimistic CTE) | Single CTE when A is funded; fallback on miss     | **39 690** | 4.8 ms  | 12.9 ms |
| 2. Single Account Debit       | Single account, all 32 goroutines competing on it | 6 484      | 21.2 ms | 32.1 ms |
| 3. Fan-out (1→1000)           | 1 source account → 1 000 recipients in one batch  | 78 867     | 499 ms  | 529 ms  |


#### `synchronous_commit=on` (full durability)


| Scenario                        | Txns/s | p99      | max      |
| ------------------------------- | ------ | -------- | -------- |
| 1. Waterfall                    | 8 986  | 5.4 ms   | 34.1 ms  |
| 1. Waterfall (optimistic CTE)   | 8 542  | 5.35 ms  | 34.78 ms |
| 2. Single Account Debit         | 945    | 163 ms   | 342 ms   |
| 3. Fan-out (1→1000)             | 37 867 | 1 138 ms | 1 188 ms |


> Single Account Debit with `synchronous_commit=on` is inherently serial: all goroutines queue
> behind one row lock, so throughput = 1 / commit_latency ≈ 945 TPS (~1.06 ms per storage ACK).
> More goroutines won't help — this is the per-account write ceiling. In production this is not
> an issue: real users never all debit the same single account simultaneously.

### TigerBeetle on local NVMe

> Measured 2026-03-25, concurrency=32, duration=15s. NVMe SSD (`nvme0n1`, 375 GB),
> PostgreSQL metadata store running as a local Docker container.


| Scenario                | Variant    | Txns/s  | p50    | p99     | max      |
| ----------------------- | ---------- | ------- | ------ | ------- | -------- |
| 1. Waterfall (batch=8)  | TB only    | 20 064  | 382 µs | 20.2 ms | 41.5 ms  |
| 1. Waterfall (batch=8)  | PG → TB    | 13 731  | 422 µs | 24.9 ms | 100.0 ms |
| 1. Waterfall            | Optimistic | 11 347  | 660 µs | 53.5 ms | 340.9 ms |
| 1. Waterfall            | Opt PG→TB  | 5 032   | 2.3 ms | 95.9 ms | 603.0 ms |
| 2. Single Account Debit | TB only    | 16 174  | 637 µs | 23.2 ms | 417.9 ms |
| 2. Single Account Debit | PG → TB    | 12 150  | 1.2 ms | 35.9 ms | 372.6 ms |
| 3. Fan-out (1→1000)     | TB only    | 206 234 | 136 µs | 698 µs  | 719 µs   |
| 3. Fan-out (1→1000)     | PG → TB    | 161 926 | 183 µs | 670 µs  | 718 µs   |


---

## Head-to-Head: AlloyDB vs TigerBeetle (TB only)

> Note: AlloyDB ran with `synchronous_commit=off` (no WAL flush before ack). TigerBeetle always
> fsyncs before acknowledging. This makes the comparison directionally useful but not apples-to-apples
> on durability guarantees.
>
> **However, AlloyDB's durability guarantee is stronger than vanilla PostgreSQL's `synchronous_commit=off`.**
> AlloyDB's storage layer synchronously writes WAL records to a low-latency regional log storage service
> (replicated across 3 zones) *before* acknowledging the commit — at the storage layer, not the Postgres
> protocol layer. A Postgres-level crash after ack will not lose data because the WAL is already durable
> in Google's distributed storage. GCP claims zero RPO within a region.
> See: [AlloyDB intelligent scalable storage](https://cloud.google.com/blog/products/databases/alloydb-for-postgresql-intelligent-scalable-storage) · [Business continuity](https://cloud.google.com/blog/products/databases/understanding-alloydb-business-continuity-capabilities)


| Scenario                   | AlloyDB `off` (PG 15) | AlloyDB `on` (PG 18) | TigerBeetle (NVMe) |
| -------------------------- | --------------------- | -------------------- | ------------------ |
| Waterfall                  | 11 434 TPS            | 8 986 TPS            | 20 064 TPS         |
| Waterfall (optimistic CTE) | **39 690 TPS**        | 8 542 TPS            | 20 064 TPS         |
| Single Account Debit       | 6 484 TPS             | 945 TPS†             | 16 174 TPS         |
| Fan-out transfers/s        | ~79 000               | ~38 000              | ~206 000           |


† Not a meaningful production number — single-account serial ceiling, not a real workload.

TigerBeetle's edge on durability-equivalent settings is much larger — AlloyDB's numbers above use
`synchronous_commit=off`, trading WAL flush for throughput. With full durability (`synchronous_commit=on`),
AlloyDB waterfall drops to ~8 986 TPS, making the realistic TB gap closer to **2.2×**. The optimistic
CTE path converges to ~8 542 TPS with sync=on — the WAL flush becomes the bottleneck regardless of
whether you take 1 step or 7, so the CTE's advantage disappears under full durability. At sync=off the
CTE's advantage is real (39 690 vs 11 434) because there is no WAL flush at all on the happy path.

---

## Key Performance Learnings

### AlloyDB / PostgreSQL

- **Optimistic CTE** (39 690 TPS, p99 4.8 ms) beats TigerBeetle's waterfall entirely by
skipping the 7-transfer linked chain when the primary account (A) is funded — which is the
common case. `BEGIN`/`COMMIT` only fires on the rare cascade miss.
- **Realistic waterfall** (11 434 TPS, p99 13 ms, max 94 ms): 2/3 of draws cascade through B
and C. The max spike to 94 ms occurs when a top-up write coincides with a deep cascade.
- **Single Account Debit contention**: simplest scenario — 1 account, 32 goroutines all trying to
debit it simultaneously. Row-level locks fully serialise access — contention is the ceiling,
not compute. 6 484 TPS at p99 21 ms.
- **Fan-out** achieves ~79k transfers/s via pgx pipeline protocol. TB is ~2.6× faster here
because its event-loop batching avoids per-row lock overhead entirely.
- **Optimistic CTE advantage disappears with `synchronous_commit=on`** (8 542 TPS, p99 5.4 ms) —
nearly identical to the realistic waterfall (8 986 TPS). With sync=on, the WAL flush is the
bottleneck for every write regardless of how few SQL steps it takes. The CTE's edge only materialises
with sync=off, where the happy path escapes the WAL flush entirely (39 690 vs 11 434 TPS).
- `**synchronous_commit=off`** is what makes the optimistic CTE shine. With `synchronous_commit=on`,
waterfall drops to ~8 986 TPS and optimistic CTE to ~8 542 TPS — consistent with TB's fsync-bound model.
Note: in AlloyDB this setting is less dangerous than in vanilla PostgreSQL — the storage layer
synchronously replicates WAL across 3 zones before ack regardless of this flag, so a Postgres-layer
crash cannot lose a committed transaction.
([GCP durability docs](https://cloud.google.com/blog/products/databases/alloydb-for-postgresql-intelligent-scalable-storage))
- **Zero errors** across all scenarios — `WHERE balance >= $1 AND depleted_at IS NULL`
prevents overdraft without constraint violations, even under maximum contention.

### TigerBeetle

- `**fsync` is the ceiling**, not CPU or network. Every doubling of fsync speed roughly
doubles throughput.
- **Local PG container overhead is larger (%) than Cloud SQL**, despite lower absolute RTT.
On NVMe, TB is so fast that even a ~0.1 ms local Postgres lookup becomes a significant
fraction of the op time:

  | Scenario               | p50 delta  | Throughput delta |
  | ---------------------- | ---------- | ---------------- |
  | Waterfall (batch=8)    | +40 µs     | −32%             |
  | Waterfall (Optimistic) | +1.6 ms    | −56%             |
  | Single Account Debit   | +0.6 ms    | −25%             |
  | Fan-out                | +47 µs/txn | −21%             |

- **Batch=8 beats Optimistic** (20 k vs 11 k TPS) by amortising one TB round-trip across 8
chains (382 µs p50 vs 660 µs). Optimistic wins only when a single transaction is isolated
and the primary account is funded.
- **Correctness test TPS: 278 732** on NVMe (batch=8189), vs ~100 k on `pd-standard`. Matches
the predicted NVMe ceiling from the hardware path table.

---

## Architecture Trade-offs

### Scale


| Scale                 | Architecture                                | Cost   | Ops overhead        |
| --------------------- | ------------------------------------------- | ------ | ------------------- |
| Small (100–1K TPS)    | Single AlloyDB cluster, simple Go service   | Low    | Minimal             |
| Medium (1K–10K TPS)   | Multi-region AlloyDB, sharded by account    | Medium | Needs dedicated SRE |
| Large (10K–100K+ TPS) | CQRS/event-sourcing, read replicas, caching | High   | Significant         |


Our 10x projection (~5 500 TPS) sits comfortably in the medium tier with ~18x headroom on
AlloyDB before any architecture change is needed.

### Entry model


| Type                  | Complexity                         | Auditability                        | Performance         |
| --------------------- | ---------------------------------- | ----------------------------------- | ------------------- |
| Simple credits/debits | Easy                               | Basic audit trail                   | Fastest             |
| Double-entry          | More complex schema                | Full audit, reconciliation built-in | ~2× writes per txn  |
| Multi-currency        | Exchange rates, precision handling | Per-currency audit trails           | Slower aggregations |


**Decision: double-entry from day one.** The reconciliation requirement (comparing against Orb)
demands a full audit trail. The write overhead (~2×) is negligible at our volume.

### Consistency model


| Model                 | Latency  | Throughput | Use case                                  |
| --------------------- | -------- | ---------- | ----------------------------------------- |
| Strong (serialisable) | Higher   | Lower      | Financial mutations — prevents overdrafts |
| Eventual              | Lower    | Higher     | Display balances, analytics               |
| Hybrid                | Balanced | Balanced   | Strong for writes, eventual for reads     |


**Decision: strong consistency for all write paths.** Billing is a legal obligation — no
"acceptable error rate." Eventual reads are fine for dashboards and analytics via read replicas.

### Billing model


| Model          | Data volume              | Complexity                        | Real-time need              |
| -------------- | ------------------------ | --------------------------------- | --------------------------- |
| Usage-based    | High (many small events) | Aggregation pipelines             | Often async batching OK     |
| Subscription   | Low                      | Recurring job scheduling          | Predictable, batch-friendly |
| Prepaid/wallet | Medium                   | Real-time balance checks critical | Must be synchronous         |
| Hybrid         | Highest                  | Most complex                      | Mixed requirements          |


Our freemium credit grants are prepaid/wallet — balance drawdown must be synchronous and
serialised per account to prevent overdrafts.

---

## What Large Companies Use


| Company       | Ledger DB                | Notes                                                                     |
| ------------- | ------------------------ | ------------------------------------------------------------------------- |
| **Stripe**    | Likely DocDB (MongoDB)   | Immutable event log, state machine — not a relational model               |
| **Uber**      | LedgerStore on Docstore  | Custom internal DB; migrated from DynamoDB, saved $6M/year                |
| **Netflix**   | MySQL                    | Billing/GL on MySQL; multi-region on CockroachDB                          |
| **Amazon**    | QLDB → Aurora PostgreSQL | QLDB deprecated 2024; AWS now recommends Aurora PG                        |
| **OpenAI**    | PostgreSQL (likely)      | Single unsharded primary + ~50 read replicas; credits use atomic DB txns  |
| **PayPal**    | AlloyDB (migrating to)   | Moving from Oracle; 3× query perf, −50% TCO                               |
| **JPMorgan**  | DB2 on z/OS mainframes   | Modernising with Thought Machine cloud core banking                       |
| **Google**    | Spanner                  | TrueTime, used for Google Ads (F1); internal financial ledger undisclosed |
| **Apple**     | FoundationDB             | Largest transactional workloads; specific ledger not confirmed            |
| **Microsoft** | Undisclosed              | Sells Azure SQL Ledger (Merkle-tree tamper-evident tables)                |


**Key takeaway:** Almost everyone builds custom ledger logic on top of battle-tested databases.
PostgreSQL is the most common choice for new systems. The trend is away from Oracle toward
PostgreSQL-compatible managed services (AlloyDB, Aurora). Companies treat ledger implementation
as proprietary — only Uber, Netflix, and Amazon have fully disclosed their architectures.

---

## Partitioning Strategy (Required from Day One)

At 10× volume (~175 M rows/month), unpartitioned tables degrade on index bloat, vacuum
pressure, and billing-period aggregations.

- **Partition key**: event timestamp (not ingestion time) — aligns with billing period
boundaries, deterministic, supports late-arriving events landing in the correct partition.
- **Granularity**: monthly — matches billing cycles.
- **Tables to partition**: usage events, ledger entries, transactions.
- **Retention**: detach old partitions → cold storage (GCS) — metadata operation, not bulk delete.

This is cheap to do at schema creation and extremely expensive to retrofit on a live table
with billions of rows.

---

## v1 Implementation Plan (8 Weeks)


| Week | Focus                                                                                                                     |
| ---- | ------------------------------------------------------------------------------------------------------------------------- |
| 1    | Schema (ledger + credit grants). Define freemium grant rules. Identify duplicate-write integration point.                 |
| 2–3  | Core ledger: double-entry, credit grant issuance/drawdown/expiration, freemium usage tracking. Partitioning from day one. |
| 4    | Freemium go-live: duplicate writes flowing, observability + alerting.                                                     |
| 5    | Export Orb pricing models + historical invoices as test fixtures. Build rating engine for v1 pricing models.              |
| 6    | Shadow mode: run in parallel with Orb, compare invoices per billing cycle.                                                |
| 7    | Fix discrepancies. Edge cases (credits, adjustments, mid-cycle changes).                                                  |
| 8    | Stabilise, finance sign-off, operational runbooks, monitoring dashboards.                                                 |


---

## Open Questions (Must Answer Before Design)

1. Which pricing models are in scope for v1? (flat-rate, per-unit, tiered, volume discount, etc.)
2. Where does the duplicate write fork into the existing event pipeline?
3. Does Orb bill by **event timestamp** or ingestion timestamp? If ingestion, every boundary
  comparison will diverge — this must be confirmed before shadow mode is meaningful.
4. What happens when a freemium user upgrades to paid mid-cycle? (proration, credit carry-over,
  which system is authoritative during the transition)
5. Exit criteria for moving paying customers fully off Orb? (define quantitatively — e.g.,
  zero unresolved discrepancies for N consecutive billing cycles, signed off by finance)

---

## Further Reading

- `[tigerbeetle-setup/README.md](tigerbeetle-setup/README.md)` — detailed TigerBeetle benchmark methodology and results
- `[learning.md](learning.md)` — AlloyDB benchmark raw results and trade-off tables
- `[docs/billing-engine-requirements.md](docs/billing-engine-requirements.md)` — full requirements, constraints, and open questions
- `[docs/ledger-database-references.md](docs/ledger-database-references.md)` — sourced references for what large companies use

