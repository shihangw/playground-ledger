# Billing Engine: Requirements & Technical Discourse

## Context

We are migrating from Orb (third-party billing engine) to an in-house usage-based billing system. This document sets the scope, constraints, and open questions for the design proposal.

Orb is not going away immediately. The new system will initially handle freemium users and shadow Orb for paying customers to build confidence before any cutover.

---

## Goals (in priority order)

1. **Ledger and credit grants for freemium users.** Track usage and manage credit/block grants for free-tier customers to unlock growth. This is the top priority and the primary business driver.
2. **Independent revenue verification.** We currently cannot verify that events we send to Orb match what Orb bills. The new system gives us an independent audit path.
3. **Shadow billing against Orb.** Run the new billing engine in parallel with Orb for paying customers. Compare invoices. Surface discrepancies — including cases where Orb may be wrong.
4. **Foundation for full Orb replacement.** Architecture should support eventual full cutover without replatform, but full migration is NOT in scope for this phase.

## Non-Goals

1. **Replacing Orb for paying customers in this phase.** Orb remains authoritative for all customer-facing invoices during the shadow period.
2. **Multi-region active-active writes.** Our throughput (~2M events/hour, projected 10x to ~20M events/hour) does not require a distributed database. Single-region with read replicas is sufficient.
3. **Evaluating alternative databases.** The data store is AlloyDB (Postgres-compatible, GCP-native). This is not up for debate — the throughput requirements are well within Postgres range, the team has Postgres expertise, our infrastructure is on GCP, and the timeline does not allow for learning a new database. See "Throughput & Scaling" section for detailed analysis.
4. **Building a generic ledger platform.** We are building a billing engine for our specific pricing models, not a general-purpose financial ledger.
5. **Custom metering pipeline.** We duplicate writes to the new system from the existing event path. No separate metering pipeline to build or maintain.

---

## Key Constraints

| Constraint | Detail |
|---|---|
| **Timeline** | 8 weeks to production (shadow mode + freemium ledger) |
| **Infrastructure** | GCP. AlloyDB for persistence. Must integrate with existing GCP tooling (IAM, VPC, Cloud Audit Logs, Pub/Sub). |
| **Billing accuracy** | Zero tolerance for invoice discrepancies on shipped invoices. Billing is a legal/contractual obligation — no "acceptable error rate." |
| **Billing period** | All cutoffs are UTC. |
| **Event timestamp** | Billing uses event timestamp (deterministic, reproducible), not ingestion timestamp. |
| **Existing system** | Orb remains the source of truth for paying customers during the shadow period. The new system must be able to reconcile against Orb output. |
| **Team** | Existing team has Postgres and Go expertise. Minimize novel operational burden. |

---

## Throughput & Scaling

### Current State

- ~2M transaction events per hour (~556 TPS)

### Design Target: 10x Current Volume

- ~20M events/hour (~5,500 TPS)

### Why AlloyDB Is Sufficient

| Metric | Our 10x Peak | AlloyDB Capability | Headroom |
|---|---|---|---|
| Write TPS | ~5,500 | 100,000+ | ~18x |
| Storage growth | ~175M rows/month | Scales with disk | No ceiling |

At 18x headroom beyond our projected peak, we are far from needing a distributed database. If we hit that ceiling (which implies ~100x growth from today), AlloyDB scaling levers available before any replatform:

1. **Table partitioning by time** — keeps query performance flat as data grows
2. **Read replicas** — offload reporting/analytics from the write path
3. **Connection pooling** (PgBouncer/Alloy-native) — maximize connection utilization
4. **CDC to BigQuery** — move historical analytics off the primary entirely

The design **must** implement time-based partitioning from day one. This is cheap to do now and extremely expensive to retrofit on a live table with billions of rows. See "Partitioning Strategy" under Key Requirements.

### Future: Agentic Commerce

The system is initially tracking metered platform usage, but the ledger and event infrastructure should support agentic commerce use cases in the future — AI agents autonomously initiating transactions, purchasing resources, and managing budgets on behalf of users. This does not change the v1 scope, but the data model should not assume that all transactions are human-initiated or follow request-response patterns. The design proposal should note how the schema accommodates autonomous, high-frequency, programmatic transactions without requiring a remodel.

---

## Key Requirements

### Event Ingestion (Duplicate Writes)

- Receive duplicated writes from the existing event pipeline (same path that sends to Orb)
- No custom metering pipeline to build — we add a write fork, not a new system
- Event deduplication with well-defined dedup window
- Handle late-arriving events with clear policy (which billing period do they land in?)
- Ability to reconcile: count of events sent vs count of events received and billed
- Retain raw events for auditability and re-rating

### Rating Engine

- Apply pricing models to metered usage to produce billable amounts
- Support the pricing models we actually use today (enumerate these — see open questions)
- Handle mid-cycle plan changes with defined proration logic
- Handle credits, adjustments, and volume discounts
- Pricing configuration must be auditable — who changed what, when

### Invoice Generation

- Produce invoice line items that can be compared against Orb invoices
- Exact-match comparison at the line-item level during shadow mode
- Support for credit notes and adjustments

### Reconciliation Engine

- Compare new system invoices against Orb invoices per customer per billing cycle
- Categorize discrepancies:
  - Event count mismatch (dropped/duplicated events)
  - Rating mismatch (different pricing logic or configuration)
  - Timing mismatch (events landing in different billing periods)
- Dashboard or alerting for discrepancy monitoring
- When discrepancies are found, determine which system matches the contract (not blindly assume Orb is correct)

### Ledger

- Double-entry bookkeeping for all financial movements
- Immutable append-only event log
- Idempotent transaction processing

### Partitioning Strategy (Required from Day One)

Time-based partitioning is a hard requirement, not an optimization. At 10x volume (~175M rows/month), unpartitioned tables degrade on:

- Index bloat and vacuum pressure
- Query performance for billing-period aggregations
- Operational tasks (backups, archival, retention enforcement)

Design considerations:

| Decision | Options | Notes |
|---|---|---|
| **Partition key** | Event timestamp (not ingestion time) | Aligns with billing period boundaries. Deterministic. |
| **Partition granularity** | Monthly | Matches billing cycles. Weekly is too many partitions to manage; quarterly is too coarse for efficient pruning. |
| **Which tables** | Usage events, ledger entries, transactions | At minimum. Any table that grows proportionally to traffic volume. |
| **Retention / archival** | Detach old partitions → cold storage (GCS) | Partitioning makes this a metadata operation, not a bulk delete. Define retention policy with finance/compliance. |
| **Late-arriving events** | Must land in the correct historical partition by event timestamp | The insert path must route to the right partition even if the event arrives weeks late. AlloyDB handles this natively with declarative partitioning. |

The designer should specify the exact `PARTITION BY RANGE` scheme and demonstrate that billing-period queries (e.g., "all usage for customer X in March") hit a single partition.

### Operational

- Observability from day one: metrics on event ingestion rate, processing lag, discrepancy counts
- Ability to re-rate a billing period from raw events (reprocessing)
- Disaster recovery plan with defined RPO/RTO

---

## Open Questions (Must Answer Before Design)

### Scope

1. **Which pricing models are in scope for v1?** List every pricing model currently configured in Orb (flat-rate, per-unit, tiered, package, BPS, volume discount, etc.). Which ones do freemium users use? Which ones can we defer to v2?
2. **How many distinct plan configurations exist in Orb today?** Can we export them?
3. **Where in the existing event path do we add the duplicate write?** Identify the integration point — is it at the API gateway, the event producer, or a Pub/Sub fan-out? This should be a minimal change to the existing pipeline.

### Reconciliation

4. **During shadow mode, what is the reconciliation unit?** Whole invoice, line item, or per-event?
5. **What is the discrepancy resolution process?** When the two systems disagree, who investigates? What's the SLA for resolution? Who decides which system is correct?
6. **Can we export historical Orb invoices with their inputs (usage events + plan config)?** These become our regression test suite.
7. **Does Orb bill by event timestamp or ingestion timestamp?** If they use ingestion timestamp, every boundary comparison will diverge — this must be confirmed before shadow mode produces meaningful results.

### Data & Compliance

8. **Data residency requirements?** Which jurisdictions? Does this affect where AlloyDB instances are deployed?
9. **Retention requirements for raw usage events and invoice records?**
10. **Who owns pricing configuration in the new system?** Sales? Finance? Product? What's the review/approval process to prevent configuration errors?

### Freemium-to-Paid Transition

14. **What happens when a freemium user upgrades to paid?** The user has usage history and possibly remaining credit grants in our ledger, but paid billing is on Orb. Key sub-questions:
    - Does the user's usage history migrate to Orb, or does Orb start fresh from the upgrade date?
    - Do remaining credit grants carry over to the paid plan? If so, how are they represented in Orb?
    - During the shadow period, who is authoritative for a recently-upgraded user — our ledger (which has history) or Orb (which now owns billing)?
    - Does the upgrade happen mid-cycle? If so, what's the proration policy for the partial free period?
15. **Is the freemium-to-paid transition a one-time event or can users downgrade back?** This affects whether we need to keep the ledger active for users who are currently on Orb.

### Cutover (Post-Phase-1, but informs architecture now)

11. **What are the exit criteria for moving paying customers off Orb?** Define quantitatively — e.g., "zero unresolved discrepancies for N consecutive billing cycles, signed off by finance."
12. **Will we migrate historical billing data from Orb or keep Orb as a read-only archive?**
13. **What is the rollback plan if the new system generates incorrect invoices after cutover?**

---

## Suggested Timeline (8 Weeks)

Priority is freemium ledger + grants first, shadow billing second.

| Week | Focus |
|---|---|
| 1 | Schema design for ledger and credit grants. Define freemium grant rules with product. Identify duplicate-write integration point in existing event pipeline. |
| 2–3 | Core ledger: double-entry bookkeeping, credit grant issuance/drawdown/expiration, usage tracking for freemium users. Table partitioning from day one. |
| 4 | Freemium go-live: duplicate writes flowing, freemium users on new ledger with credit grants. Observability and alerting. |
| 5 | Export Orb pricing models + historical invoices as test fixtures. Build rating engine for v1 pricing models. |
| 6 | Shadow mode: run billing for paying customers in parallel with Orb, compare invoices. |
| 7 | Fix discrepancies from shadow mode. Edge cases (credits, adjustments, mid-cycle changes). |
| 8 | Stabilize, finance sign-off on shadow results, operational runbooks, monitoring dashboards. |

---

## What This Document Is For

This document is the input to the design proposal, not the design itself. A senior engineer should use this to:

1. Confirm or challenge the scope and constraints
2. Propose a system design that satisfies the requirements within the constraints
3. Explicitly address each open question or flag which ones block the design
4. Present a more detailed breakdown of the 8-week timeline with milestones and exit criteria per phase
