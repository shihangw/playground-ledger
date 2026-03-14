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
