PG Ledger Benchmark Results (2026-03-24, AlloyDB, concurrency=32, duration=15s each)

| Scenario | Description | Jobs | Jobs/s | p50 | p99 | p999 | Errors |
|---|---|---|---|---|---|---|---|
| 1 — Waterfall | Independent accounts, 4-account groups, no contention | 87 137 | 5 809 | 4.3 ms | 20.8 ms | 37.4 ms | 0 |
| 2 — Hot account | All 32 goroutines debit the same source account | 69 617 | 4 641 | 5.1 ms | 34.6 ms | 73.2 ms | 0 |
| 3 — Fan-out | 1 batch call = 1 000 deposits; 71 667 transfers/s | 1 075 | 72 | 438.4 ms | 739.6 ms | 788.7 ms | 0 |

PG Benchmark Learnings

- Baseline single-row write throughput (S1): ~5 800 ops/s at p50 4.3 ms — competitive with TigerBeetle's ~4 100 ops/s for the same no-contention pattern.
- Hot-account contention cost (S1 → S2): −20% throughput, +1 ms p50, p99 degrades 1.7× (20.8 → 34.6 ms). PG row-level locks serialise goroutines but the throughput drop is bounded — not catastrophic at 32 goroutines.
- p999 tail is tight at low contention (37.4 ms S1) but widens under contention (73.2 ms S2), indicating lock-wait queuing at the tail.
- Batch fan-out (S3): 71 667 individual transfers/s vs TigerBeetle's ~97 000. PG batch commit overhead (~440 ms p50) is the bottleneck, not network. TB's event-loop batching is ~35% more efficient for high-fan-out workloads.
- Zero errors across all scenarios — PG serialisable isolation correctly prevents overdrafts even under maximum hot-account contention.
- p99 and p999 in S1/S2 are TB-bound in the TB setup; in PG they are commit-latency-bound — both systems show similar tail shapes for single-row ops.

Scale Trade-offs

| Scale                 | Architecture Complexity                            | Cost   | Operational Overhead |
| --------------------- | -------------------------------------------------- | ------ | -------------------- |
| Small (100-1K TPS)    | Single CockroachDB cluster, simple Go service      | Low    | Minimal              |
| Medium (1K-10K TPS)   | Multi-region CockroachDB, sharded by account       | Medium | Need dedicated SRE   |
| Large (10K-100K+ TPS) | CQRS/event-sourcing, read replicas, caching layers | High   | Significant ops team |

Entry Types Trade-offs

| Type                  | Complexity                                          | Auditability                        | Performance                |
| --------------------- | --------------------------------------------------- | ----------------------------------- | -------------------------- |
| Simple credits/debits | Easy to implement                                   | Basic audit trail                   | Fastest                    |
| Double-entry          | More complex schema, balancing logic                | Full audit, reconciliation built-in | ~2x writes per transaction |
| Multi-currency        | Exchange rates, precision handling, complex queries | Per-currency audit trails           | Slower aggregations        |

Consistency Trade-offs

| Model    | Latency                    | Throughput              | Use Case                                                |
| -------- | -------------------------- | ----------------------- | ------------------------------------------------------- |
| Strong   | Higher (serializable txns) | Lower (more contention) | Financial-critical, prevents overdrafts                 |
| Eventual | Lower                      | Higher                  | Display balances, analytics                             |
| Hybrid   | Balanced                   | Balanced                | Best of both - strong for mutations, eventual for reads |

Billing Model Trade-offs

| Model          | Data Volume              | Complexity                        | Real-time Needs             |
| -------------- | ------------------------ | --------------------------------- | --------------------------- |
| Usage-based    | High (many small events) | Aggregation pipelines needed      | Often async batching OK     |
| Subscription   | Low                      | Recurring job scheduling          | Predictable, batch-friendly |
| Prepaid/wallet | Medium                   | Real-time balance checks critical | Must be synchronous         |
| Hybrid         | Highest                  | Most complex, but most flexible   | Mixed requirements          |
