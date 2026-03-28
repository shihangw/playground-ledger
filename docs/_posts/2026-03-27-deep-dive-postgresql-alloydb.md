---
layout: post
title: "Deep Dive: PostgreSQL on AlloyDB — 39,690 TPS with the Right Patterns"
date: 2026-03-27
---

<script src="https://cdn.jsdelivr.net/npm/chart.js"></script>

PostgreSQL is the boring choice. That's why we picked it. But "boring" doesn't mean "slow" — with the right patterns, we hit 39,690 TPS on a single AlloyDB cluster for our most critical billing path. This article covers the specific implementation tactics that got us there, what PostgreSQL is genuinely good at for ledger workloads, and where it hits its limits.

---

## How We Got Here

We started with the simplest possible implementation: a double-entry ledger schema on AlloyDB, wrapping every waterfall drawdown in an explicit `BEGIN...COMMIT` transaction. That got us 5,809 TPS. Not bad, but not competitive with TigerBeetle's 20K.

Then we iterated. Each optimization built on the last, and the final numbers tell the story:

<canvas id="iterationChart" width="800" height="400"></canvas>
<script>
new Chart(document.getElementById('iterationChart'), {
  type: 'bar',
  data: {
    labels: ['Baseline\n(BEGIN...COMMIT)', '+ depleted_at\npartial index', '+ Optimistic CTE\n(fast path)', 'Realistic cascade\n(mixed workload)'],
    datasets: [{
      label: 'Throughput (TPS)',
      data: [11705, 11349, 39690, 11434],
      backgroundColor: [
        'rgba(54, 162, 235, 0.4)',
        'rgba(54, 162, 235, 0.55)',
        'rgba(54, 162, 235, 0.85)',
        'rgba(54, 162, 235, 0.7)'
      ],
      borderColor: 'rgba(54, 162, 235, 1)',
      borderWidth: 1
    }]
  },
  options: {
    responsive: true,
    plugins: {
      title: { display: true, text: 'Waterfall Throughput: Optimization Journey', font: { size: 16 } },
      legend: { display: false }
    },
    scales: { y: { beginAtZero: true, title: { display: true, text: 'Transactions per Second' } } }
  }
});
</script>

| Iteration | Technique | Txns/s | p99 | max |
|---|---|---|---|---|
| Baseline | `BEGIN...COMMIT` wrapping all 4 accounts | 11,705 | 10.9 ms | 22.2 ms |
| + `depleted_at` | Partial index to skip exhausted accounts | 11,349 | 11.2 ms | 23.8 ms |
| + Optimistic CTE | Single CTE for primary; txn on miss | **39,690** | **4.8 ms** | **12.9 ms** |
| Realistic cascade | 2/3 draws cascade through B and C | 11,434 | 13.0 ms | 93.9 ms |

**Test hardware**: AlloyDB N2, 32 vCPU, 256 GB RAM, `us-central1`, private IP, `synchronous_commit=off`, `lock_timeout=200ms`, concurrency=32, duration=15s.

---

## Tactic 1: The Optimistic CTE (The Biggest Win)

The single biggest performance improvement came from rethinking when we need a transaction at all.

**The insight**: Most of the time, the user's primary credit grant (account A) has funds. We only need to cascade when A is depleted. So why pay the cost of `BEGIN...COMMIT` for the common case?

The optimistic CTE combines the balance check, debit, and ledger entry write into a single SQL statement with no explicit transaction:

```sql
WITH debit AS (
    UPDATE accounts
    SET balance    = balance - $1,
        updated_at = now(),
        depleted_at = CASE WHEN balance - $1 = 0 THEN now() ELSE NULL END
    WHERE id = $2
      AND balance >= $1
      AND depleted_at IS NULL
    RETURNING balance
)
INSERT INTO ledger_entries (account_id, entry_type, amount, balance_after)
SELECT $2, 'DEBIT', $1, balance FROM debit
```

If the CTE's `UPDATE` matches a row (account has funds), the `INSERT` fires, and PostgreSQL auto-commits the whole thing. If the `WHERE` clause fails (insufficient balance or account depleted), nothing happens — zero rows affected, no error, no wasted lock.

```go
func waterfallDraw(ctx context.Context, pool *pgxpool.Pool,
    accounts []uuid.UUID, amount decimal.Decimal) bool {

    // Fast path: single CTE, no explicit transaction
    tag, err := pool.Exec(ctx, waterfallOptimisticSQL, amount, accounts[0])
    if err == nil && tag.RowsAffected() == 1 {
        return true  // Done — no BEGIN, no COMMIT, no lock contention
    }

    // Slow path: explicit transaction over remaining accounts
    if len(accounts) <= 1 {
        return false
    }
    return waterfallOneTxn(ctx, pool, accounts[1:], amount)
}
```

**Why 3.5x faster**: The auto-committed CTE avoids three round-trips (`BEGIN`, the query, `COMMIT`) and — critically — avoids holding a row lock for the duration of a transaction. With 32 concurrent goroutines, this eliminates almost all lock contention on the fast path.

### The Caveat: synchronous_commit Matters

This 3.5x advantage only holds with `synchronous_commit=off`. With `synchronous_commit=on`, the optimistic CTE drops to 8,542 TPS — nearly identical to the full waterfall (8,986 TPS). The WAL flush becomes the bottleneck regardless of how few SQL steps you take.

<canvas id="syncChart" width="800" height="350"></canvas>
<script>
new Chart(document.getElementById('syncChart'), {
  type: 'bar',
  data: {
    labels: ['Waterfall', 'Optimistic CTE'],
    datasets: [
      {
        label: 'sync=off',
        data: [11434, 39690],
        backgroundColor: 'rgba(54, 162, 235, 0.7)',
        borderColor: 'rgba(54, 162, 235, 1)',
        borderWidth: 1
      },
      {
        label: 'sync=on',
        data: [8986, 8542],
        backgroundColor: 'rgba(255, 99, 132, 0.7)',
        borderColor: 'rgba(255, 99, 132, 1)',
        borderWidth: 1
      }
    ]
  },
  options: {
    responsive: true,
    plugins: {
      title: { display: true, text: 'synchronous_commit: The Great Equalizer', font: { size: 16 } },
      legend: { position: 'bottom' }
    },
    scales: { y: { beginAtZero: true, title: { display: true, text: 'TPS' } } }
  }
});
</script>

**On AlloyDB, `synchronous_commit=off` is safer than it sounds.** AlloyDB's storage layer synchronously replicates WAL records across 3 zones before acknowledging the write — at the storage layer, not the Postgres protocol layer. A Postgres-level crash after ack will not lose data because the WAL is already durable in Google's distributed storage. GCP claims zero RPO within a region.

---

## Tactic 2: The `depleted_at` Skip Pattern

When a credit grant is fully consumed, we don't want to even attempt a row lock on it. The `depleted_at` column with a partial index lets the waterfall skip exhausted accounts entirely:

```sql
-- Schema
ALTER TABLE accounts ADD COLUMN depleted_at TIMESTAMPTZ;
CREATE INDEX idx_accounts_depleted ON accounts(id) WHERE depleted_at IS NULL;

-- Query only touches accounts with funds
WHERE id = $2 AND balance >= $1 AND depleted_at IS NULL
```

When the balance hits zero, the debit query atomically sets `depleted_at`:

```sql
depleted_at = CASE WHEN balance - $1 = 0 THEN now() ELSE NULL END
```

And on top-up, it clears:

```sql
UPDATE accounts SET balance = $1, depleted_at = NULL, updated_at = now()
WHERE id = $2
```

**The partial index** (`WHERE depleted_at IS NULL`) is smaller than a full index because it excludes depleted accounts. This means faster lookups and less memory pressure — especially important when you have millions of accounts but only a fraction are active.

**Why it doesn't help throughput much** (11,349 vs 11,705 TPS): In our benchmark, the waterfall cycles through depletion and top-up every 150 draws. The `depleted_at` skip is only useful for the ~50 draws after account A depletes and before the next top-up. The real benefit is **avoiding wasted lock attempts** on accounts that will never fund — more impactful in production where some grants expire permanently.

---

## Tactic 3: Pipeline Protocol for Batch Operations

For fan-out scenarios (one source account → 1,000 recipients), individual `INSERT` statements hit per-transaction overhead hard. The pgx pipeline protocol lets us batch 1,000 operations into a single TCP round-trip:

```go
pgConn := conn.Conn().PgConn()
pipeline := pgConn.StartPipeline(ctx)

for i, op := range ops {
    pipeline.SendQueryParams(depositAtomicSQL,
        [][]byte{[]byte(op.Amount.String()), []byte(op.AccountID.String()), {}},
        []uint32{0, 0, 0}, []int16{0, 0, 0}, []int16{0, 0},
    )
    // Insert Sync boundary every 10 ops
    if (i+1)%batchSyncGroup == 0 || i == len(ops)-1 {
        pipeline.SendPipelineSync()
    }
}
pipeline.Flush()  // One TCP round-trip for all 1000 ops
```

This achieves **78,867 transfers/s** — competitive with TigerBeetle's 206K (TB is ~2.6x faster due to event-loop batching, but Postgres is in the same order of magnitude).

### The Error Isolation Trick

The `batchSyncGroup=10` is critical. In PostgreSQL's pipeline protocol, an error puts the connection into an error state. Without Sync boundaries, one failed operation (say, a lock timeout on op #5) would poison all remaining results:

```
Op 1: OK
Op 2: OK
...
Op 5: LOCK TIMEOUT → connection enters error state
Op 6: ERROR (poisoned)
Op 7: ERROR (poisoned)
...
Op 1000: ERROR (poisoned)
```

With `SendPipelineSync()` every 10 ops, the error state clears at each boundary. At most 10 ops are affected per failure, not 1,000.

---

## Tactic 4: Lock Ordering for Grant Waterfall

The full grant drawdown path is more complex than the simple balance debit. It needs to:

1. Lock the user's account (for cash balance fallback)
2. Lock all active credit grants in FIFO order
3. Consume grants by priority, then by expiration
4. Fall back to cash balance if grants are exhausted
5. Write audit entries for every grant touched

The critical detail is **lock ordering** — without it, you get deadlocks:

```go
// Step 1: Lock account FIRST (global ordering prevents deadlocks)
tx.QueryRow(ctx,
    `SELECT balance FROM accounts WHERE id = $1 FOR UPDATE`,
    accountID,
).Scan(&cashBalance)

// Step 2: Lock grants in deterministic order (priority ASC, expires_at ASC)
rows, _ := tx.Query(ctx,
    `SELECT id, remaining_amount FROM credit_grants
     WHERE account_id = $1 AND status = 'ACTIVE'
       AND remaining_amount > 0 AND expires_at > now()
     ORDER BY priority ASC, expires_at ASC
     FOR UPDATE`,
    accountID,
)

// Step 3: Consume grants FIFO
for _, grant := range grants {
    consumed := min(grant.Remaining, remaining)
    tx.Exec(ctx, `UPDATE credit_grants SET remaining_amount = remaining_amount - $1 ...`)
    tx.Exec(ctx, `INSERT INTO grant_ledger_entries ...`)
    remaining -= consumed
    if remaining == 0 { break }
}

// Step 4: Fall back to cash
if remaining > 0 {
    tx.Exec(ctx, `UPDATE accounts SET balance = balance - $1 ...`)
    tx.Exec(ctx, `INSERT INTO ledger_entries ...`)
}

tx.Commit(ctx)
```

**Why lock ordering matters**: If goroutine A locks the account then grant G1, while goroutine B locks grant G1 then the account, you deadlock. By always locking the account first, then grants in a deterministic order (priority, then expiry), all goroutines acquire locks in the same sequence.

---

## Tactic 5: Schema Evolution for Performance

Our schema evolved through 6 migrations, and the performance-critical ones teach useful lessons:

### Remove the CHECK constraint (Migration 004)

```sql
-- REMOVED:
CONSTRAINT balance_non_negative CHECK (balance >= 0)
```

**Why**: The `CHECK` constraint fires after the row is modified. Our `WHERE balance >= $1` clause already prevents overdrafts — the UPDATE simply matches zero rows if the balance is insufficient. The CHECK constraint adds overhead for a guarantee we already have.

### Decouple audit from hot path (Migration 004)

```sql
ALTER TABLE ledger_entries ALTER COLUMN transaction_id DROP NOT NULL;
ALTER TABLE ledger_entries ALTER COLUMN idempotency_key DROP NOT NULL;
```

**Why**: The hot-path CTE (deposit/withdraw) writes directly to `ledger_entries` without creating a `transactions` row. The transaction record is an audit artifact, not a hot-path requirement. Making these columns nullable lets the CTE skip the `transactions` table entirely.

### Add grant priority (Migration 005)

```sql
ALTER TABLE credit_grants ADD COLUMN priority INT NOT NULL DEFAULT 0;
CREATE INDEX idx_grants_account_active ON credit_grants(
    account_id, priority ASC, expires_at ASC
) WHERE status = 'ACTIVE';
```

**Why**: Different grant types have different consumption priorities. Daily credits (priority=0) should be consumed before monthly grants (priority=1), regardless of expiration date. The composite index ensures the database returns grants in the correct consumption order without a sort operation.

---

## What PostgreSQL Is Good At

### The Full SQL Ecosystem

You can `EXPLAIN ANALYZE` your queries. You can run ad-hoc analytics directly. You can use `pg_stat_statements` to find slow queries. You can `pg_dump` for backups. The monitoring, tooling, and institutional knowledge around PostgreSQL is unmatched.

For a billing system, this matters enormously. When finance asks "show me all transactions for customer X in March," you write a SQL query. When there's a discrepancy, you `JOIN` ledger entries against credit grants against transactions. You don't need a separate OLAP database for basic operational queries.

### Schema Flexibility

Our schema evolved through 6 migrations in two weeks. Adding `depleted_at`, removing the CHECK constraint, making columns nullable, adding grant priorities — all done with `ALTER TABLE` on a live system. Try that with TigerBeetle's fixed schema of accounts and transfers.

### Team Velocity

Our team writes Go + SQL every day. The waterfall logic is plain SQL — any backend engineer can read it, debug it, and optimize it. The optimistic CTE pattern is a well-documented PostgreSQL technique. There's no new paradigm to learn.

### The Optimistic Path Performance

When the common case is fast (and it almost always is in prepaid billing — users usually have funds), the optimistic CTE achieves **39,690 TPS at p99 4.8 ms**. This is faster than TigerBeetle's waterfall and more than sufficient for our projected load.

---

## What PostgreSQL Is Not Good At

### Single-Account Contention

When 32 goroutines all debit the same account, PostgreSQL serializes them behind row-level locks:

| | AlloyDB | TigerBeetle |
|---|---|---|
| **Single Account Debit TPS** | 6,484 | 16,174 |
| **p99** | 21.2 ms | 23.2 ms |

TigerBeetle is 2.5x faster because its event-loop model doesn't have locks at all. In PostgreSQL, contention is the ceiling — more goroutines won't help, they'll just queue longer.

**In practice this is acceptable**: Enterprise hot accounts with thousands of concurrent users are rare, and even 6,484 TPS per account is well above any realistic per-customer load.

### Durability vs Throughput Tradeoff

With `synchronous_commit=on`, all our optimizations converge to ~8,500-9,000 TPS. The WAL flush (~1 ms) dominates everything else. TigerBeetle also fsyncs on every write, but its single-threaded event loop batches multiple operations per fsync, making each flush more efficient.

AlloyDB's storage architecture mitigates this (WAL is replicated across 3 zones at the storage layer regardless of the Postgres-level setting), but it's still a real tradeoff to understand.

### The Transaction Overhead

Every explicit `BEGIN...COMMIT` incurs:
1. A round-trip for `BEGIN`
2. Lock acquisition (blocks if contended)
3. Lock hold time (entire transaction duration)
4. A round-trip for `COMMIT`
5. WAL flush (if sync=on)

The optimistic CTE eliminates steps 1-4, which is why it's 3.5x faster. But when you *need* a transaction (the cascade path), you pay the full cost. Any workload that's predominantly cascade (multiple depleted accounts) will converge toward the baseline ~11K TPS.

---

## Recipe: Freemium Credit Drawdown on AlloyDB

Here's the complete recipe for implementing the waterfall on PostgreSQL:

1. **Schema**: Double-entry ledger with `accounts`, `credit_grants`, `ledger_entries`, and `grant_ledger_entries`. Monthly partitioning on `created_at` from day one. Partial indexes on `depleted_at IS NULL` and `status = 'ACTIVE'`.

2. **Fast path** (99% of requests): Single optimistic CTE that debits the highest-priority account. No explicit transaction. Auto-commits on success, no-ops on insufficient balance.

3. **Slow path** (1% of requests): Explicit `BEGIN...COMMIT` that cascades through remaining accounts in priority order. Lock account first, then grants in deterministic order (priority ASC, expires_at ASC).

4. **Grant consumption**: FIFO by priority, then by expiration. Consume from the cheapest (most about to expire) grant first. Write audit trail to `grant_ledger_entries`.

5. **Batch operations**: Use pgx pipeline protocol with `batchSyncGroup=10` for error isolation. Achieves 78K+ TPS for fan-out scenarios.

6. **Tuning**: `synchronous_commit=off` on AlloyDB (safe due to storage-layer replication), `lock_timeout=200ms` to fail fast, partial indexes to skip depleted accounts.

7. **Observability**: Per-phase timing histograms (idempotency check, lock acquisition, DB writes, commit). Alert on p99 > 50ms.

---

## The Bottom Line

PostgreSQL on AlloyDB gives us **39,690 TPS on the fast path** and **11,434 TPS on the realistic mixed workload** — with zero errors, full double-entry bookkeeping, and ad-hoc SQL queryability. It's 18x above our projected peak load.

TigerBeetle is faster for contention-heavy and bulk workloads. But PostgreSQL is fast *enough*, and the operational simplicity — one database, one query language, one team's existing expertise — is worth more than 2x throughput we don't need.

We're shipping on AlloyDB. If we ever outgrow it, the double-entry model ports cleanly to TigerBeetle, and we've already benchmarked the migration path.

---

*This is Part 3 of a 3-part series. See also: [Building a Billing Ledger]({% post_url 2026-03-27-building-a-billing-ledger %}) and [Deep Dive: TigerBeetle]({% post_url 2026-03-27-deep-dive-tigerbeetle %}).*
