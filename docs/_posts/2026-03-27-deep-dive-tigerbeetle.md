---
layout: post
title: "Deep Dive: TigerBeetle — A Purpose-Built Financial Transactions Database"
date: 2026-03-27
---

<script src="https://cdn.jsdelivr.net/npm/chart.js"></script>
<script src="https://cdn.jsdelivr.net/npm/mermaid@10/dist/mermaid.min.js"></script>
<script>mermaid.initialize({startOnLoad:true, theme:'neutral'});</script>

TigerBeetle is a database built from scratch for one thing: financial transactions. No SQL. No query planner. No general-purpose flexibility. Just double-entry bookkeeping at the speed of `fsync`. This article covers how we benchmarked it, the key implementation tactics that make it fast, what it's genuinely good at, and where it falls short.

---

## How We Got Here

We started this evaluation knowing our billing ledger needs to handle waterfall-style credit drawdowns at scale. The question was: can a purpose-built financial database outperform PostgreSQL enough to justify the operational complexity?

We set up TigerBeetle on a GCP `e2-standard-4` (4 vCPU, 16 GB RAM) with a 375 GB local NVMe SSD. We paired it with a PostgreSQL container for metadata (user → account mappings) and ClickHouse for OLAP analytics via a CDC pipeline. The benchmark harness is a Go program using `tigerbeetle-go` v0.16.78, running 32 concurrent goroutines for 15 seconds per scenario.

The architecture looks like this:

<div class="mermaid">
graph LR
    A[Go Service] -->|user lookup| B[(PostgreSQL<br/>metadata)]
    A -->|write txns| C[(TigerBeetle<br/>ledger)]
    C -->|poll event log| D[CDC Sidecar]
    D -->|streaming insert| BQ[(BigQuery)]
    D -.->|or: publish| E[LavinMQ] -.-> CH[(ClickHouse)]

    style C fill:#ffd8a8,stroke:#f59e0b
    style B fill:#a5d8ff,stroke:#4a9eed
    style BQ fill:#a5d8ff,stroke:#4a9eed
    style CH fill:#c3fae8,stroke:#22c55e
    style D fill:#d0bfff,stroke:#8b5cf6
    style E fill:#d0bfff,stroke:#8b5cf6
    style A fill:#a5d8ff,stroke:#4a9eed
</div>

---

## The Numbers

In production, every TigerBeetle operation needs a PostgreSQL metadata lookup first (user → account ID mapping). **The PG→TB numbers are the realistic production numbers.** We show TB-only for comparison, but you should plan capacity around PG→TB.

<canvas id="tbThroughput" width="800" height="400"></canvas>
<script>
new Chart(document.getElementById('tbThroughput'), {
  type: 'bar',
  data: {
    labels: ['Waterfall\n(batch=8)', 'Waterfall\n(Optimistic)', 'Single Account\nDebit', 'Fan-out\n(1→1000)'],
    datasets: [
      {
        label: 'PG → TB (production realistic)',
        data: [13731, 5032, 12150, 161926],
        backgroundColor: 'rgba(255, 159, 64, 0.7)',
        borderColor: 'rgba(255, 159, 64, 1)',
        borderWidth: 1
      },
      {
        label: 'TB Only (synthetic)',
        data: [20064, 11347, 16174, 206234],
        backgroundColor: 'rgba(255, 159, 64, 0.3)',
        borderColor: 'rgba(255, 159, 64, 1)',
        borderWidth: 1
      }
    ]
  },
  options: {
    responsive: true,
    plugins: {
      title: { display: true, text: 'TigerBeetle Throughput (TPS) — Production (PG→TB) vs Synthetic (TB Only)', font: { size: 16 } },
      legend: { position: 'bottom' }
    },
    scales: { y: { beginAtZero: true, title: { display: true, text: 'Transactions per Second' } } }
  }
});
</script>

| Scenario | Variant | Txns/s | p50 | p99 | max |
|---|---|---|---|---|---|
| Waterfall (batch=8) | **PG → TB** | **13,731** | 422 µs | 24.9 ms | 100.0 ms |
| Waterfall (batch=8) | TB only | 20,064 | 382 µs | 20.2 ms | 41.5 ms |
| Waterfall (optimistic) | **PG → TB** | **5,032** | 2.3 ms | 95.9 ms | 603.0 ms |
| Waterfall (optimistic) | TB only | 11,347 | 660 µs | 53.5 ms | 340.9 ms |
| Single Account Debit | **PG → TB** | **12,150** | 1.2 ms | 35.9 ms | 372.6 ms |
| Single Account Debit | TB only | 16,174 | 637 µs | 23.2 ms | 417.9 ms |
| Fan-out (1→1000) | **PG → TB** | **161,926** | 183 µs | 670 µs | 718 µs |
| Fan-out (1→1000) | TB only | 206,234 | 136 µs | 698 µs | 719 µs |

> **Why PG→TB is the real number**: TigerBeetle stores accounts and transfers — nothing else. User IDs, grant metadata, pricing plans, account priorities — all of that lives in PostgreSQL. Every production request starts with a PG lookup to resolve "user X" into "TB account IDs [A, B, C, D]" before submitting the transfer batch. Even with a local PG container at ~0.1 ms RTT, this adds 21–56% overhead depending on the scenario.

---

## Tactic 1: Linked Transfer Chains (Atomic Waterfall)

TigerBeetle doesn't have SQL transactions. Instead, it has **linked transfers** — a sequence of transfers where all succeed or all fail atomically. This is the primitive we use to build the waterfall.

Our waterfall uses a 7-transfer linked chain to debit from 4 source accounts in priority order. This mimics our freemium billing model where a user has multiple credit grants with different lifetimes:

- **Account A** — Daily credit ($5, refreshed every 24h, consumed first)
- **Account B** — Monthly credit ($5, refreshed every billing cycle)
- **Account C** — Bonus/promotional credit ($5, one-time grant)
- **Account D** — Cash balance ($100K, safety net, never topped up)

When a user makes an API call costing $0.10, the chain tries A first. If A is depleted (e.g., after 50 calls), it cascades to B, then C, then D.

```
Transfer 1: SETUP → LIMIT       (establish withdrawal ceiling)  [Linked]
Transfer 2: A → SETUP           (try daily credit)              [Linked + BalancingDebit/Credit]
Transfer 3: B → SETUP           (try monthly credit)            [Linked + BalancingDebit/Credit]
Transfer 4: C → SETUP           (try bonus credit)              [Linked + BalancingDebit/Credit]
Transfer 5: D → SETUP           (try cash balance)              [Linked + BalancingDebit/Credit]
Transfer 6: SETUP → DESTINATION (deliver funds to usage account) [Linked]
Transfer 7: LIMIT → SETUP       (reset ceiling)                 [BalancingCredit only — NOT linked]
```

The magic is in the `BalancingDebit` and `BalancingCredit` flags. When set, TigerBeetle automatically adjusts the transfer amount to respect the source account's available balance. If account A has $3 but we're drawing $5, it takes the $3 and the next transfer in the chain picks up the remaining $2 from B. The server enforces all of this — **no application-level balance reads required**.

```go
bal := types.TransferFlags{Linked: true, BalancingDebit: true, BalancingCredit: true}.ToUint16()
return append(batch,
    types.Transfer{ID: types.ID(), DebitAccountID: a, CreditAccountID: setup,
        Amount: T, Ledger: 1, Code: 1, Flags: bal},
    types.Transfer{ID: types.ID(), DebitAccountID: b, CreditAccountID: setup,
        Amount: T, Ledger: 1, Code: 1, Flags: bal},
    // ...
)
```

**Why this matters**: In PostgreSQL, implementing this same waterfall requires `SELECT ... FOR UPDATE`, checking balances, updating rows, and committing — multiple round-trips under an explicit transaction. TigerBeetle does it in a single network call with server-side enforcement.

---

## Tactic 2: Batch Amortization

The single most important performance lever in TigerBeetle is **batching**. Each call to `CreateTransfers()` incurs one network round-trip and one `fsync`. By packing multiple logical operations into one batch, you amortize that fixed cost.

<canvas id="batchChart" width="800" height="300"></canvas>
<script>
new Chart(document.getElementById('batchChart'), {
  type: 'line',
  data: {
    labels: ['1', '8', '253 (dev max)', '8189 (prod max)'],
    datasets: [{
      label: 'Throughput (TPS)',
      data: [2600, 20064, 40000, 97853],
      borderColor: 'rgba(255, 159, 64, 1)',
      backgroundColor: 'rgba(255, 159, 64, 0.2)',
      fill: true,
      tension: 0.3
    }]
  },
  options: {
    responsive: true,
    plugins: {
      title: { display: true, text: 'Waterfall TPS vs Batch Size', font: { size: 16 } },
      legend: { display: false }
    },
    scales: {
      x: { title: { display: true, text: 'Chains per Batch' } },
      y: { beginAtZero: true, title: { display: true, text: 'TPS' } }
    }
  }
});
</script>

| Batch Size | Txns/s | p50/txn |
|---|---|---|
| 1 (unbatched) | ~2,600 | ~5 ms |
| **8 (our default)** | **20,064** | **382 µs** |
| 253 (dev mode max) | ~40,000 | — |
| 8189 (prod mode max) | ~97,853 | — |

Going from batch=1 to batch=8 delivers a **7.7x throughput improvement**. The per-transaction latency drops from 5 ms to 382 µs because the fsync cost is shared across 8 waterfall chains (56 transfers total).

This is why **batch=8 beats the optimistic path** (20K vs 11K TPS). The optimistic path sends one transfer first, then falls back to a 7-transfer chain on miss. Even though the optimistic path avoids unnecessary transfers when account A is funded, it pays the full fsync cost for a single transfer. Batching 8 chains into one fsync wins on amortization.

```go
// Build batch of 8 waterfall chains (56 transfers total)
batch := make([]types.Transfer, 0, 8*7)
for i := 0; i < 8; i++ {
    batch = appendChain(batch, ch.sources[0], ch.sources[1],
        ch.sources[2], ch.sources[3], ch.dest, ch.setup, ch.limit)
}
results, err := client.CreateTransfers(batch)  // one network call, one fsync
```

---

## Tactic 3: Account Flags as Business Logic

TigerBeetle pushes balance enforcement into the database itself through account flags:

```go
constrained := types.AccountFlags{DebitsMustNotExceedCredits: true}.ToUint16()
accounts = append(accounts, types.Account{
    ID: types.ToUint128(accountID),
    Flags: constrained,
    Ledger: 1, Code: 1,
})
```

With `DebitsMustNotExceedCredits`, any transfer that would overdraw the account is rejected at the server level. No pre-read. No optimistic locking. No race condition. The database **is** the business rule.

This eliminates an entire class of bugs: the "check-then-act" race where two goroutines both read a balance of $5, both approve a $3 debit, and the account goes to -$1. In TigerBeetle, the second transfer simply fails with `TransferExceedsCredits`.

---

## Tactic 4: Two-Phase Transfers (Pending/Post/Void)

For scenarios where you need to reserve funds before settling (e.g., authorization holds), TigerBeetle has built-in two-phase transfers:

```go
// Phase 1: Reserve $200 (pending — funds are held but not settled)
pending := types.Transfer{
    ID: pendingID,
    DebitAccountID: payer,
    CreditAccountID: payee,
    Amount: types.ToUint128(200),
    Flags: types.TransferFlags{Pending: true}.ToUint16(),
    Ledger: 1, Code: 1,
}

// Phase 2a: Settle the full amount
settle := types.Transfer{
    PendingID: pendingID,
    Flags: types.TransferFlags{PostPendingTransfer: true}.ToUint16(),
}

// Phase 2b: Or void (cancel the reservation)
void := types.Transfer{
    PendingID: pendingID,
    Flags: types.TransferFlags{VoidPendingTransfer: true}.ToUint16(),
}
```

This maps directly to billing scenarios like:
- **Metered billing**: Reserve estimated usage at start of period, settle actual usage at end
- **Enterprise invoicing**: Hold funds on PO approval, settle on invoice finalization

---

## What TigerBeetle Is Good At

### Contention-Heavy Workloads

With the PG metadata lookup included, TigerBeetle sustains **12,150 TPS on a single hot account** with 32 concurrent goroutines — zero errors, no deadlocks, no lock timeouts. PostgreSQL hits 6,484 TPS on the same test and is fundamentally limited by row-level lock serialization. That's still a **1.9x advantage** in the realistic scenario.

TigerBeetle's event-loop architecture processes all transfers sequentially within a single thread, eliminating contention entirely. There are no locks because there's no concurrency at the data level — the server serializes everything at the IO boundary.

### Bulk Operations

Fan-out (1 source → 1,000 recipients) hits **161,926 transfers/s** with PG lookup (206K TB-only) — still 2x faster than PostgreSQL's pipeline protocol. Each 1000-transfer batch completes with sub-millisecond per-transaction latency (183 µs p50) because the batch is processed as a single fsync operation.

### Correctness Guarantees

Every transfer is fsync'd before acknowledgment. Every account balance is checked server-side. Linked chains provide atomic multi-step operations. There's no way to overdraft, no way to lose a committed transfer, and no `synchronous_commit=off` escape hatch to worry about.

---

## What TigerBeetle Is Not Good At

### No Ad-Hoc Queries

There's no SQL. You can't `SELECT * FROM transfers WHERE amount > 100 AND created_at > '2026-03-01'`. If you need analytics, reporting, or any query that isn't "look up account" or "create transfer," you need a separate system.

Our solution: a CDC pipeline (TigerBeetle → LavinMQ → ClickHouse) that streams transfer events to an OLAP store. This adds operational complexity — now you're running three databases instead of one.

#### How the CDC Pipeline Works

<div class="mermaid">
sequenceDiagram
    participant TB as TigerBeetle
    participant CDC as CDC Sidecar
    participant MQ as LavinMQ (AMQP)
    participant CH as ClickHouse

    loop Every 1s or 2730 events
        CDC->>TB: Poll event log (tb amqp)
        TB-->>CDC: Batch of transfer events
        CDC->>MQ: Publish to fanout exchange
    end

    MQ->>CH: RabbitMQ engine consumes
    CH->>CH: Materialized view inserts<br/>into transfers (MergeTree)

    Note over CH: Monthly partitions<br/>ORDER BY (ledger, event_time, transfer_id)<br/>TTL: 7 years
</div>

Each CDC event carries the full transfer context — not just the transfer itself, but the **account balance snapshots at event time** for both the debit and credit accounts. This means ClickHouse can reconstruct any account's balance at any point in history without replaying the log.

```json
{
  "type": "single_phase",
  "transfer": { "id": "...", "amount": 500, "code": 1 },
  "debit_account": {
    "id": 9000010,
    "debits_posted": 500,
    "credits_posted": 0
  },
  "credit_account": {
    "id": 9000011,
    "debits_posted": 0,
    "credits_posted": 500
  }
}
```

The key design principle: **the CDC pipeline is async and non-blocking**. The write path (TigerBeetle) never waits for the analytics store. The CDC sidecar polls TB's event log independently. Analytics can lag minutes behind without affecting billing accuracy.

#### Why CDC, Not Double-Write?

An alternative to CDC is having the application write to both TigerBeetle and the analytics store directly. We chose CDC because:

- **No dual-write consistency risk.** If the app writes to TB successfully but the BigQuery/ClickHouse write fails, your data is inconsistent. With CDC, TigerBeetle's event log is the single source of truth — the analytics store is just a projection.
- **No hot-path impact.** The app only writes to TB. Analytics ingestion happens out-of-band.
- **Full rebuild from scratch.** If the analytics store is lost, replay the entire TB event log. With double-write, lost events are gone forever.
- **Simpler app code.** No retry logic, no error handling for two write targets.

The only advantage of double-write is fewer moving parts (no sidecar, no broker). But if you make the second write async to avoid latency impact, you're essentially reinventing CDC — with worse guarantees.

#### Where Should CDC Write To? ClickHouse vs BigQuery

The CDC sidecar can target different analytics stores. The choice depends on your query latency needs:

<div class="mermaid">
graph LR
    TB[(TigerBeetle)] -->|poll event log| CDC[CDC Sidecar]
    CDC -->|"Option A"| MQ[LavinMQ] --> CH[(ClickHouse)]
    CDC -->|"Option B"| BQ[(BigQuery)]

    style TB fill:#ffd8a8,stroke:#f59e0b
    style CDC fill:#d0bfff,stroke:#8b5cf6
    style MQ fill:#d0bfff,stroke:#8b5cf6
    style CH fill:#c3fae8,stroke:#22c55e
    style BQ fill:#a5d8ff,stroke:#4a9eed
</div>

| | ClickHouse (self-hosted) | BigQuery (serverless) |
|---|---|---|
| **Ingestion latency** | Sub-second (MergeTree INSERT) | Seconds (streaming API) |
| **Query latency** | Sub-second on hot data | Seconds (serverless cold start) |
| **Ops burden** | You run it (upgrades, disk, monitoring) | Zero — fully managed |
| **Cost at 500 TPS** | ~$50-150/mo (compute + storage) | ~$10-30/mo (storage + $5/TB queried) |
| **Cost at 5K TPS** | ~$500-1K/mo | ~$50-100/mo (scales better) |
| **Cold storage** | Detach to GCS, query via `s3()` | Native — BQ *is* cold storage that's queryable |
| **GCP native** | No (run on GCE) | Yes (IAM, audit logs, Data Studio) |
| **Best for** | Real-time dashboards, live alerting | Batch reconciliation, compliance audits, finance |

**For our use case** — reconciliation against the existing billing system, monthly finance audits, compliance queries — **BigQuery is the simpler choice**. We don't need sub-second dashboard queries. The pipeline simplifies to:

```
TigerBeetle → CDC sidecar → BigQuery (streaming insert API)
```

No LavinMQ, no ClickHouse to operate. One fewer service. If we later need real-time dashboards, we can add ClickHouse as a second CDC consumer without changing the write path.

#### Data Durability: How We Avoid Data Loss

Regardless of whether CDC targets ClickHouse or BigQuery, the durability model is the same:

<div class="mermaid">
graph TD
    A[TigerBeetle Event Log] -->|"Source of truth<br/>immutable, fsync'd"| B{CDC Sidecar<br/>crashes?}
    B -->|"Yes"| C["Restart: resume from<br/>last committed offset<br/>TB log is immutable —<br/>no events lost"]
    B -->|"No"| D{Analytics store<br/>down?}
    D -->|"Yes"| E["Sidecar retries<br/>or LavinMQ buffers<br/>until store recovers"]
    D -->|"No"| F["INSERT succeeds<br/>— durable"]

    style A fill:#ffd8a8,stroke:#f59e0b
    style C fill:#c3fae8,stroke:#22c55e
    style E fill:#fff3bf,stroke:#f59e0b
    style F fill:#c3fae8,stroke:#22c55e
</div>

**TigerBeetle's event log is the ultimate safety net.** It's immutable and fsync'd — the same log that guarantees transfer durability also guarantees CDC completeness. Events are never deleted from TB's log.

The specific guarantees:

1. **TigerBeetle → CDC Sidecar**: The sidecar tracks its read offset. On crash/restart, it resumes from the last committed position. TB's event log is append-only — the sidecar can always catch up.

2. **CDC Sidecar → Analytics store**: The sidecar delivers at-least-once. If it crashes after writing but before advancing its offset, it re-sends on restart. The analytics store must handle deduplication (keyed on transfer ID).

3. **Analytics store durability**: BigQuery streaming inserts are durable once acknowledged. ClickHouse MergeTree INSERTs are durable on disk. Both handle deduplication — BigQuery via `INSERT ... WHERE NOT EXISTS`, ClickHouse via `ReplacingMergeTree`.

**The worst case**: If the entire analytics store is lost, replay TigerBeetle's event log from the beginning. This is the same guarantee event-sourced architectures provide — the event log is the source of truth, everything else is a projection.

#### Cost Summary

| Component | Cost Driver | Estimate at 500 TPS |
|---|---|---|
| **TigerBeetle** | NVMe storage (open source) | ~$75/mo (375 GB local NVMe on GCP) |
| **CDC Sidecar** | CPU (runs alongside TB) | ~$0 marginal |
| **BigQuery (recommended)** | Storage + query | ~$10-30/mo |
| **ClickHouse (if real-time needed)** | Compute + storage | ~$50-150/mo |
| **LavinMQ (ClickHouse only)** | Transient messages | ~$0 marginal |

### Metadata Lives Elsewhere

TigerBeetle stores accounts and transfers. It doesn't store users, products, pricing models, or any domain metadata. In our benchmark, every operation that needs user → account mappings requires a PostgreSQL lookup first.

The overhead is measurable:

<canvas id="pgOverheadChart" width="800" height="300"></canvas>
<script>
new Chart(document.getElementById('pgOverheadChart'), {
  type: 'bar',
  data: {
    labels: ['Waterfall\n(batch=8)', 'Waterfall\n(Optimistic)', 'Single Account\nDebit', 'Fan-out'],
    datasets: [{
      label: 'Throughput Reduction from PG Lookup',
      data: [32, 56, 25, 21],
      backgroundColor: 'rgba(255, 99, 132, 0.7)',
      borderColor: 'rgba(255, 99, 132, 1)',
      borderWidth: 1
    }]
  },
  options: {
    responsive: true,
    plugins: {
      title: { display: true, text: 'Throughput Penalty from PostgreSQL Metadata Lookup (%)', font: { size: 16 } },
      legend: { display: false }
    },
    scales: { y: { beginAtZero: true, max: 60, title: { display: true, text: '% Reduction' } } }
  }
});
</script>

The optimistic waterfall path takes a 56% throughput hit from the PG lookup because TigerBeetle is so fast that even a 0.1 ms local Postgres query becomes a significant fraction of the total operation time.

### Operational Learning Curve

TigerBeetle is not PostgreSQL. There's no `pg_dump`, no `EXPLAIN ANALYZE`, no ecosystem of monitoring tools. The concepts are different (ledgers, codes, linked chains, balancing transfers). The team needs to learn a new mental model for data operations that they've been doing in SQL for years.

### The fsync Ceiling

TigerBeetle's throughput is bounded by `fsync` latency. This is by design — it never compromises on durability — but it means your storage hardware determines your ceiling:

| Storage | fsync Latency | Approx. TB Throughput |
|---|---|---|
| `pd-standard` (HDD) | ~1–2 ms | ~50–100K transfers/s |
| `pd-ssd` | ~0.5 ms | ~200K transfers/s |
| Local NVMe (our bench) | ~50–100 µs | ~200–500K transfers/s |
| Bare-metal NVMe | ~20–50 µs | 1M+ transfers/s |

Each doubling of fsync speed roughly doubles throughput. You can't tune your way around slow disks.

---

## Recipe: Freemium Credit Drawdown on TigerBeetle

If we were building the freemium waterfall entirely on TigerBeetle, here's the recipe:

1. **Account setup**: One TB account per credit grant, flagged `DebitsMustNotExceedCredits`. One cash account (no flag). SETUP and LIMIT control accounts for the linked chain.

2. **Grant issuance**: Transfer from a global funding bank to the grant account. The credit balance *is* the grant balance.

3. **Usage drawdown**: Submit a 7-transfer linked chain (the waterfall pattern above). TB automatically drains from A→B→C→D in order, respecting balances.

4. **Batch for throughput**: Pack 8 waterfall chains per `CreateTransfers()` call. With PG metadata lookup included, this gets you to **~13.7K TPS** on NVMe — still 27x headroom over 500 TPS.

5. **Grant expiration**: Transfer remaining balance from the grant account back to the funding bank. Mark the account closed in your PostgreSQL metadata store.

6. **Analytics**: CDC pipeline to BigQuery (recommended) or ClickHouse for reporting, reconciliation, and compliance queries. BigQuery is zero-ops and GCP-native; ClickHouse if you need real-time dashboards.

**Trade-off**: You get ~2x the throughput of AlloyDB on the same hardware (13.7K vs 11.4K TPS on realistic waterfall), with stronger correctness guarantees. You pay for it with a more complex operational stack (PG for metadata + TB for ledger + BigQuery/ClickHouse for analytics) and the inability to query your financial data directly from the ledger.

---

## The fsync Insight

The most important thing we learned from benchmarking TigerBeetle: **fsync is the ceiling, not CPU or network.** This has profound implications for capacity planning:

- Don't bother adding more CPUs — TigerBeetle is single-threaded by design
- Don't optimize your network — the fsync latency dominates
- **Do** invest in the fastest NVMe storage you can get
- **Do** maximize batch sizes to amortize the fsync cost

In production with 3-node cluster replication (VSR consensus), you pay an additional ~0.5–1 ms cross-zone RTT for quorum. Even so, a 3-node NVMe cluster will outperform a single node on `pd-standard` because the local fsync on cheap disks is slower than cross-zone replication on fast disks.

---

*Next: [Deep Dive: PostgreSQL on AlloyDB]({{ site.baseurl }}{% post_url 2026-03-27-deep-dive-postgresql-alloydb %}) — how we achieved 39,690 TPS with optimistic CTEs, and why PostgreSQL might be all you need.*
