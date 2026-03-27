---
layout: post
title: "Building a Large-Scale Billing Ledger: Why, What, and How We Evaluated"
date: 2026-03-27
categories: [architecture, billing]
---

<script src="https://cdn.jsdelivr.net/npm/chart.js"></script>

We're building an in-house billing ledger to power the next phase of our growth. This article explains why we're doing it, what the system needs to support, and how we prototyped and benchmarked two very different database architectures on GCP to find the right recipe.

---

## The Scale We're Designing For

Our starting point is **2 million transactions per hour** — roughly 500 TPS. That's manageable today. But we're not designing for today. We want a system that can **theoretically scale 100x** to support further growth within 1-2 year without re-platforming for such critical system.

That means our architecture needs a credible path from 500 TPS to 50,000 TPS — even if we never get there. The question isn't "can it handle today's load?" but "at what point does the architecture break, and what does the next architecture look like?"


| Growth Stage       | TPS Target  | Architecture Implication                                          |
| ------------------ | ----------- | ----------------------------------------------------------------- |
| Today              | ~500 TPS    | Single cluster, simple Go service                                 |
| 10x (Year 1-2)     | ~5,000 TPS  | Partitioning, read replicas, connection pooling                   |
| 100x (Theoretical) | ~50,000 TPS | Both TigerBeetle and AlloyDB handle this on a single node         |


The 100x row is the key insight: **both databases we evaluated can handle ~50K TPS on a single cluster**. TigerBeetle reaches 20K TPS on waterfall workloads (higher with batching), and AlloyDB hits ~40K TPS with optimistic patterns. We don't need sharding, CQRS, or multi-region writes to reach our theoretical ceiling. This dramatically simplifies the architecture — and it's what gave us the confidence to move forward.

---

## What the System Needs to Do

This isn't a generic ledger. It's a **usage-based billing (UBB) engine** with very specific functional requirements. Every API call a customer makes generates a billable event. Those events must be metered, rated against the customer's pricing plan, and debited from their balance — millions of times per hour.

### Freemium Users with Credits and Grants

Our freemium tier gives users credit grants — signup bonuses, promotions, monthly allowances. These grants have **priorities**, **expiration dates**, and must be consumed in a specific order (FIFO by priority, then by expiry).

When a user makes an API call, the system must:

1. Check active credit grants in priority order
2. Debit from the highest-priority, earliest-expiring grant first
3. Cascade to the next grant if the first is depleted
4. Fall back to the user's cash balance if all grants are exhausted
5. Do all of this **atomically** — no overdrafts, no double-spending

This is the **waterfall pattern**, and it's the most performance-critical path in the entire system.

### Near-Real-Time Balance Checks

Every API call needs a balance check, but it doesn't need to be perfectly synchronous. **Sub-second balance availability is acceptable** — if a user briefly goes slightly negative due to concurrent requests, that's fine. This is a billing system, not a stock exchange. We can reconcile small overages after the fact rather than blocking every request behind a serialized balance read.

This relaxation is a major performance unlock: it means we can use read replicas or cached balances for the check, and only the debit write path needs strong consistency. The difference between "must block on exact balance" and "sub-second staleness is OK" is the difference between 6,484 TPS (serialized hot account) and 39,690 TPS (optimistic fast path).

### Enterprise Accounts

Enterprise customers have thousands of users under a single billing account. This means:

- **Hot accounts**: Many concurrent users drawing from the same balance
- **High contention**: Row-level locks or equivalent under heavy concurrent access

### Grant Expiration and Clawback (Fan-out)

Credit grants expire. Promotions get revoked. When that happens, the system needs to bulk-expire or claw back thousands of grants in a single operation — zero out remaining balances, write audit entries, update statuses. This is a fan-out workload and it needs to be fast enough to run without blocking the hot path.

### Future Extension: Transfers

Account-to-account transfers (e.g., payouts, refunds) are not an immediate requirement for our UBB engine, but the architecture should support them without re-platforming.

### Double-Entry Bookkeeping

If you're not familiar with double-entry: every financial movement produces two entries — a debit from one account and an equal credit to another. The total debits in the system must always equal total credits. If they don't, something is wrong and you can find it. This is the same principle that banks have used for 500 years, and it's what makes it possible to answer "where did the money go?" for any transaction at any point in time.

Every financial movement must produce balanced debit and credit entries. This isn't optional — it's how we reconcile against the existing billing system and how finance audits the books. The write overhead (~2x) is negligible at our volume compared to the auditability it provides.

---

## What We Built: A GCP Prototype

Rather than debating architectures in a design doc, we built working prototypes and benchmarked them. The goal was to **de-risk the project** — understand the performance characteristics, failure modes, and operational trade-offs of each approach before committing. We evaluated two fundamentally different approaches:

### Option A: TigerBeetle

TigerBeetle is a purpose-built financial transactions database. It's designed from the ground up for exactly this use case — double-entry bookkeeping with strict consistency guarantees. It uses deterministic simulation testing and always fsyncs before acknowledging.

**Test hardware**: GCP `e2-standard-4` (4 vCPU, 16 GB RAM) with 375 GB local NVMe SSD.

### Option B: AlloyDB (PostgreSQL-Compatible)

AlloyDB is Google Cloud's PostgreSQL-compatible managed database. We evaluated it because:

- Team has deep Postgres expertise
- PostgreSQL is the most common choice for new ledger systems (OpenAI, PayPal, Amazon's QLDB replacement all use Postgres)
- AlloyDB's storage layer provides stronger durability than vanilla Postgres — WAL records are synchronously replicated across 3 zones before acknowledgment

**Test hardware**: AlloyDB N2, 32 vCPU, 256 GB RAM, `us-central1`, private IP.

### Three Benchmark Scenarios

We designed scenarios that map directly to our production workload:


| Scenario                 | Models                         | Why It Matters                                       |
| ------------------------ | ------------------------------ | ---------------------------------------------------- |
| **Waterfall**            | Freemium credit drawdown (UBB) | The hot path — every API call hits this              |
| **Single Account Debit** | Enterprise hot account         | Worst-case contention — 32 threads, 1 account        |
| **Fan-out (1→1000)**     | Grant expiration/clawback      | Bulk expire or claw back thousands of grants at once |


---

## The Results: Head-to-Head

<canvas id="waterfallChart" width="800" height="400"></canvas>
<script>
new Chart(document.getElementById('waterfallChart'), {
  type: 'bar',
  data: {
    labels: ['Waterfall', 'Waterfall\n(Optimistic)', 'Single Account\nDebit', 'Fan-out\n(transfers/s)'],
    datasets: [
      {
        label: 'TigerBeetle (NVMe)',
        data: [20064, 20064, 16174, 206234],
        backgroundColor: 'rgba(255, 159, 64, 0.7)',
        borderColor: 'rgba(255, 159, 64, 1)',
        borderWidth: 1
      },
      {
        label: 'AlloyDB (sync=off)',
        data: [11434, 39690, 6484, 78867],
        backgroundColor: 'rgba(54, 162, 235, 0.7)',
        borderColor: 'rgba(54, 162, 235, 1)',
        borderWidth: 1
      },
      {
        label: 'AlloyDB (sync=on)',
        data: [8986, 8542, 945, 37867],
        backgroundColor: 'rgba(54, 162, 235, 0.3)',
        borderColor: 'rgba(54, 162, 235, 1)',
        borderWidth: 1,
        borderDash: [5, 5]
      }
    ]
  },
  options: {
    responsive: true,
    plugins: {
      title: { display: true, text: 'Throughput Comparison (TPS)', font: { size: 16 } },
      legend: { position: 'bottom' }
    },
    scales: {
      y: {
        beginAtZero: true,
        title: { display: true, text: 'Transactions per Second' }
      }
    }
  }
});
</script>

<canvas id="latencyChart" width="800" height="400"></canvas>
<script>
new Chart(document.getElementById('latencyChart'), {
  type: 'bar',
  data: {
    labels: ['Waterfall', 'Waterfall\n(Optimistic)', 'Single Account\nDebit', 'Fan-out\n(per batch)'],
    datasets: [
      {
        label: 'TigerBeetle p99',
        data: [20.2, 53.5, 23.2, 0.698],
        backgroundColor: 'rgba(255, 159, 64, 0.7)',
        borderColor: 'rgba(255, 159, 64, 1)',
        borderWidth: 1
      },
      {
        label: 'AlloyDB p99',
        data: [13.0, 4.8, 21.2, 499],
        backgroundColor: 'rgba(54, 162, 235, 0.7)',
        borderColor: 'rgba(54, 162, 235, 1)',
        borderWidth: 1
      }
    ]
  },
  options: {
    responsive: true,
    plugins: {
      title: { display: true, text: 'p99 Latency (ms)', font: { size: 16 } },
      legend: { position: 'bottom' }
    },
    scales: {
      y: {
        beginAtZero: true,
        title: { display: true, text: 'Milliseconds' }
      }
    }
  }
});
</script>


### Key Takeaways

**TigerBeetle dominates on raw throughput and contention handling.** Fan-out hits 206K vs 79K TPS. Under maximum contention (single hot account, 32 threads), TigerBeetle sustains 16K TPS vs AlloyDB's 6.5K. Its event-loop architecture has no row locks — contention simply doesn't exist at the data layer. Built-in balance enforcement and linked transfer chains mean the database *is* the business logic for financial invariants.

**AlloyDB is competitive on the optimized common case.** The optimistic CTE pattern — a single SQL statement that tries the primary account without an explicit transaction — achieves 39,690 TPS. This beats TigerBeetle's waterfall (20K TPS) because most draws succeed on the first account, avoiding the 7-transfer linked chain entirely. The trade-off: this relies on `synchronous_commit=off`.

**The durability knob matters differently by user segment.** With `synchronous_commit=on`, AlloyDB's optimistic advantage disappears (8,542 TPS). For freemium credit drawdowns, losing a fraction of a cent of free credit in a crash is acceptable. For enterprise and paid users, real money is at stake — a purpose-built database that always fsyncs before ack removes this trade-off entirely.

**Both databases have more than enough headroom.** The critical de-risking outcome: at 500 TPS today, both options give us 20–80x headroom. We're not choosing between "fast enough" and "too slow" — we're choosing between two architectures that both work, with different operational trade-offs.





---

## What Large Companies Use

We researched what the industry looks like. The pattern is clear: **everyone builds custom ledger logic on top of battle-tested databases.**


| Company     | Finance DB              | Key Detail                                              |
| ----------- | ----------------------- | ------------------------------------------------------- |
| **Stripe**  | Undisclosed             | Ledger processes [5B events/day](https://stripe.dev/blog/ledger-stripe-system-for-tracking-and-validating-money-movement) as immutable log; underlying DB not disclosed |
| **Uber**    | LedgerStore on Docstore | Powers payment platform; [migrated from DynamoDB, saved $6M/year](https://www.uber.com/blog/dynamodb-to-docstore-migration/) |
| **Netflix** | MySQL                   | [Billing transactions, subscriptions, taxes, and revenue](https://blog.bytebytego.com/p/ep60-netflix-tech-stack-databases) |
| **OpenAI**  | Undisclosed (likely PG) | Atomic DB txns for [credit balance + usage billing](https://openai.com/index/beyond-rate-limits/); serialized per account |
| **Amazon**  | QLDB (deprecated)       | [QLDB shut down 2025](https://www.infoq.com/news/2024/07/aws-kill-qldb/); AWS recommends Aurora PG as replacement |

Most companies treat their ledger database as proprietary and don't disclose it. Uber and Netflix are the most transparent — their engineering blogs detail the full architecture. Stripe publishes the ledger design but not the underlying database.

---

## What We Learned

The benchmarking exercise de-risked the project in three critical ways:

### 1. Both databases handle our scale — no exotic architecture needed

At 500 TPS today with a 100x theoretical target, both TigerBeetle and AlloyDB have massive headroom on a single cluster. No sharding, no CQRS, no multi-region writes. This means we can start simple and stay simple for a long time.

### 2. The waterfall pattern works on both — with different trade-offs

- **TigerBeetle**: 7-transfer linked chains with server-side balance enforcement. 20K TPS with batch=8. The database enforces the financial invariants — no application-level balance reads needed.
- **AlloyDB**: Optimistic CTE for the fast path, explicit transaction fallback for cascades. Up to 40K TPS with `synchronous_commit=off`. More SQL flexibility, but the application owns the business logic.

### 3. AlloyDB's query observability accelerated the optimization process

We didn't start at 40K TPS. Our first AlloyDB implementation — a straightforward `BEGIN...COMMIT` wrapping all 4 accounts — hit ~5,800 TPS. The journey from 5.8K to 39.7K was driven by PostgreSQL's built-in observability: `EXPLAIN ANALYZE` showed us where lock waits dominated, `pg_stat_statements` revealed which queries were hot, and per-phase timing histograms in our benchmark harness pinpointed that `BEGIN`/`COMMIT` round-trips were the bottleneck, not the actual writes. That's what led us to the optimistic CTE pattern — eliminating the explicit transaction on the fast path. This kind of iterative, data-driven optimization is much harder with a purpose-built database that doesn't expose a query planner.

### 4. Durability guarantees differ by design

TigerBeetle always fsyncs — you get correctness by default. AlloyDB gives you a knob (`synchronous_commit`) to trade durability for throughput. For freemium credits, that trade-off is acceptable. For enterprise billing, you want the guarantee built into the database.

### Architecture Principles (Database-Agnostic)

Regardless of which database powers the ledger, these principles hold:

- **Double-entry bookkeeping from day one** — every mutation produces balanced debit/credit entries with `balance_after` snapshots
- **Monthly partitioning from day one** — cheap to set up, expensive to retrofit on a live table with billions of rows
- **Batch operations for fan-out** — grant expiration/clawback needs to process thousands of accounts without blocking the hot path
- **Immutable audit trail** — ledger entries are append-only, partitions detach to cold storage (GCS)

---

## What's Next

The next two articles in this series go deep on each database:

- **[Deep Dive: TigerBeetle]({% post_url 2026-03-27-deep-dive-tigerbeetle %})** — linked transfer chains, batching strategies, the fsync ceiling, and how to build a waterfall on a purpose-built financial database.
- **[Deep Dive: PostgreSQL on AlloyDB]({% post_url 2026-03-27-deep-dive-postgresql-alloydb %})** — optimistic CTEs, pipeline protocol batching, grant waterfall with lock ordering, and why `synchronous_commit=off` is safer than you think on AlloyDB.

All benchmark code, schemas, and Docker setups are open source: **[github.com/shihangw/playground-ledger](https://github.com/shihangw/playground-ledger)**

We built prototypes. We benchmarked them. The numbers de-risked the project and told us what's possible. The rest of this series shows exactly how each database works under the hood.