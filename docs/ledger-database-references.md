# What Large Companies Use for Their Ledger & Database Systems

## Stripe

- Custom **double-entry ledger** system called "Ledger" processing 5 billion events/day, built as an immutable log with state machine representation [1]
- **DocDB** — custom database-as-a-service built on **MongoDB Community**, serving 5 million queries/sec across 2,000+ shards with petabytes of financial data [2][3]
- Custom Go-based database proxy for reliability, scalability, and access control [2]
- Achieved 99.999% uptime while processing $1 trillion in payments (2023) [2]
- **Ledger DB: Not explicitly confirmed, but likely not a relational DB** — Ledger is described as an immutable log of events with state machine representation [1], which is an event-sourcing/append-only pattern rather than a relational model. Given DocDB (MongoDB) is Stripe's primary operational DB [2], the Ledger likely runs on DocDB or a similar document/event store

**References:**

1. [Ledger: Stripe's system for tracking and validating money movement](https://stripe.dev/blog/ledger-stripe-system-for-tracking-and-validating-money-movement) — Stripe Dev Blog
2. [How Stripe's document databases supported 99.999% uptime with zero-downtime data migrations](https://stripe.com/blog/how-stripes-document-databases-supported-99.999-uptime-with-zero-downtime-data-migrations) — Stripe Blog
3. [How Stripe Scaled to 5 Million Database Queries Per Second](https://blog.bytebytego.com/p/how-stripe-scaled-to-5-million-database) — ByteByteGo
4. [Stripe's DocDB at QCon SF 2025](https://qconsf.com/presentation/nov2025/stripes-docdb-how-zero-downtime-data-movement-powers-trillion-dollar-payment) — QCon

---

## PayPal

- Migrating from legacy **Oracle** databases to **Google Cloud** managed databases: **AlloyDB for PostgreSQL**, **Cloud SQL for PostgreSQL**, **Cloud Spanner**, and **BigQuery** [1]
- Reduced database TCO by ~50%, with up to 3x query performance improvements [1]
- **Ledger DB: Not publicly disclosed** — migrating general infra from Oracle to Google Cloud, but which DB backs the ledger specifically is not confirmed

**References:**

1. [PayPal Bets Its AI Future on a Massive Google Cloud Database Migration](https://www.webpronews.com/paypal-bets-its-ai-future-on-a-massive-google-cloud-database-migration-and-the-payoff-is-already-showing/) — WebProNews

---

## Amazon

- Built **QLDB** (Quantum Ledger Database) — originated from an internal need for an immutable, searchable transaction log to track every data plane change within AWS [1]
- **QLDB is now deprecated**, with AWS recommending migration to **Aurora PostgreSQL** [4]
- Finance Automation uses **DynamoDB** for sub-ledger data + **Neptune** (graph DB) for account relationships + **OpenSearch** for search [3]
- **Ledger DB: Confirmed — QLDB (internal version) [1], DynamoDB for sub-ledgers [3]**

**References:**

1. [Amazon QLDB](https://aws.amazon.com/qldb/) — AWS
2. [Building a core banking system with Amazon QLDB](https://aws.amazon.com/blogs/industries/building-a-core-banking-system-with-amazon-quantum-ledger-database/) — AWS Blog
3. [How Amazon Finance Automation built an operational data store](https://aws.amazon.com/blogs/database/how-amazon-finance-automation-built-an-operational-data-store-with-aws-purpose-built-databases-to-power-critical-finance-applications/) — AWS Database Blog
4. [AWS Discontinues QLDB](https://www.infoq.com/news/2024/07/aws-kill-qldb/) — InfoQ
5. [Dynamo paper (SOSP 2007)](https://www.allthingsdistributed.com/files/amazon-dynamo-sosp2007.pdf) — Werner Vogels

---

## eBay

- **Oracle** (450 nodes) + **Cassandra** (2000+ NoSQL nodes), handling 400 billion DB calls/day [3]
- Cassandra for time-series fraud detection, order tracking, mobile notifications; Oracle for core RDBMS workloads [1][2]
- **Ledger DB: Not publicly disclosed** — no public info on their internal financial ledger system specifically

**References:**

1. [Cassandra Data Modeling Best Practices at eBay](https://tech.ebayinc.com/engineering/cassandra-data-modeling-best-practices-part-1/) — eBay Tech Blog
2. [Cassandra at eBay - Scale (PDF)](https://docs.huihoo.com/apache/cassandra/planetcassandra/Cassandra-at-eBay-Scale.pdf) — Planet Cassandra
3. [Inside eBay Tech Stack](https://appscrip.com/blog/ebay-tech-stack-and-infrastructure/) — Appscrip

---

## Walmart

- Uses **Google Cloud Spanner** for globally distributed, horizontally scalable workloads [1]
- 25,000+ engineers supporting 10,500 stores and thousands of fulfillment centers [2]
- **Ledger DB: Not publicly disclosed** — uses "Vision Suite" for accounting but no engineering details on ledger DB

**References:**

1. [How Walmart's Data Platform Handles 100x Scale with Cloud Spanner](https://newsletter.enginuity.software/p/walmart-data-platform-with-cloud-spanner) — Enginuity
2. [Walmart Global Tech Blog](https://tech.walmart.com/content/walmart-global-tech/en_us/blog/post.html) — Walmart
3. [Walmart Global Tech - Medium](https://medium.com/walmartglobaltech) — Medium

---

## Banks (JPMorgan Chase, Goldman Sachs)

- **JPMorgan**: Mainframes process ~$10 trillion in payments/day [1]. 32 data centers, 450+ PB of data, 6,500+ applications [1]. Also investing in blockchain (**Onyx/Kinexys**) for settlement [3]
- **JPMorgan Ledger DB: Not confirmed** — JPMorgan uses IBM DB2 on z/OS mainframes (evidenced by job listings, not official engineering sources [2]), but no official source confirms DB2 is specifically the ledger database. Modernizing with **Thought Machine** (cloud-based core banking) [4]
- **Goldman Sachs**: Built **GS DAP** (Digital Asset Platform) for digital bond issuance; participating in **Canton Network** (blockchain of blockchains) for cross-institution ledger synchronization [3]
- **Goldman Sachs Ledger DB: Not publicly disclosed**

**References:**

1. [The IT wingspan of JPMorgan Chase](https://www.datacenterdynamics.com/en/analysis/the-it-wingspan-of-jpmorgan-chase-co/) — DCD
2. [Why Mainframes Still Matter in Banking](https://www.fintechweekly.com/magazine/articles/why-mainframes-still-matter-in-banking-digital-era-interview-with-jennifer-nelson) — FinTech Weekly
3. [How JPMorgan, Goldman Sachs Are Building Blockchain Finance Infrastructure](https://mskadu.medium.com/beyond-bitcoin-how-jpmorgan-goldman-sachs-and-s-p-are-quietly-building-blockchain-finance-358e9c5a7be1) — Medium
4. [JPMorgan Chase moving retail bank's core system to cloud](https://www.americanbanker.com/news/jpmorgan-chase-moving-retail-banks-core-system-to-cloud) — American Banker

---

## OpenAI

- **PostgreSQL** as core database — single unsharded primary with ~50 read replicas, serving 800M+ ChatGPT users [1]
- **PgBouncer** for connection pooling (cut latency from 50ms to 5ms) [1]
- Write-heavy workloads migrated to **Azure Cosmos DB** (sharded) [2]
- Achieved five-nines availability [1]
- **Billing/Credits system**: Built in-house real-time access engine combining rate limits + credits [5]. Three separate datasets: product usage events, monetization events, and balance updates. Balance updates are **asynchronous** (near-real-time, not synchronous) — intentionally trading slight delay for provable correctness [5]. Credit balance decrease + Balance Update record inserted in a **single atomic database transaction**, serialized per account [5]. **Specific DB not named** in the article — likely PostgreSQL given [1] but not explicitly confirmed for this system

**References:**

1. [Scaling PostgreSQL to power 800 million ChatGPT users](https://openai.com/index/scaling-postgresql/) — OpenAI
2. [How OpenAI Scaled to 800M Users With Postgres](https://blog.bytebytego.com/p/how-openai-scaled-to-800-million) — ByteByteGo
3. [OpenAI Scales Single Primary PostgreSQL Instance](https://www.infoq.com/news/2026/02/openai-runs-chatgpt-postgres/) — InfoQ
4. [How OpenAI scaled with Azure Database for PostgreSQL](https://www.microsoft.com/en-us/startups/blog/openai-and-postgresql-scaling-with-microsoft-azure/) — Microsoft
5. [Beyond rate limits: scaling access to Codex and Sora](https://openai.com/index/beyond-rate-limits/) — OpenAI

---

## Google

- **Spanner** — originally built internally for Google Ads (F1 database) [1], uses TrueTime (atomic clocks) for global consistency
- Spanner is also used for Gmail, Google Photos, and other Google services [1]
- **Ledger DB: Not publicly disclosed** — Spanner is confirmed for Google Ads, but Google's internal financial/accounting ledger DB is not disclosed

**References:**

1. [Spanner (database) — Wikipedia](https://en.wikipedia.org/wiki/Spanner_(database))
2. [How Spanner became a global, mission-critical database](https://cloud.google.com/blog/products/gcp/from-nosql-to-new-sql-how-spanner-became-a-global-mission-critical-database) — Google Cloud Blog

---

## Apple

- **FoundationDB** — distributed, ACID-compliant key-value store. Apple acquired it in 2015, open-sourced in 2018 [1][2]
- Scales to Apple's "largest transactional workloads" [1]
- Custom programming language **Flow** (actor-based concurrency on C++11) built for FoundationDB [3]
- **Ledger DB: Not publicly disclosed** — FoundationDB is confirmed for large transactional workloads but Apple does not disclose which systems specifically

**References:**

1. [FoundationDB — Wikipedia](https://en.wikipedia.org/wiki/FoundationDB)
2. [apple/foundationdb — GitHub](https://github.com/apple/foundationdb)
3. [FoundationDB: A Distributed Unbundled Transactional Key Value Store (paper)](https://www.foundationdb.org/files/fdb-paper.pdf)

---

## Netflix

- **MySQL** for billing transactions, subscriptions, taxes, and revenue — migrated from licensed Oracle to open-source MySQL on EC2 [1][2]
- Master-master MySQL setup with synchronous replication using InnoDB [2]
- **CockroachDB** adopted starting 2020, now 380+ production clusters. Used for multi-region active-active architecture and global transactions [3][4]
- Batch jobs create recurring renewal orders; aggregated data feeds into General Ledger (GL) for daily revenue [1]
- **Ledger DB: Confirmed — MySQL** for billing/GL. CockroachDB for newer multi-region workloads [1][3]

**References:**

1. [Netflix Billing Migration to AWS](https://netflixtechblog.com/netflix-billing-migration-to-aws-451fba085a4) — Netflix TechBlog
2. [EP60: Netflix Tech Stack - Databases](https://blog.bytebytego.com/p/ep60-netflix-tech-stack-databases) — ByteByteGo
3. [Now Streaming: Why Netflix Runs a Fleet of 380+ CockroachDB Clusters](https://www.cockroachlabs.com/customers/netflix/) — Cockroach Labs
4. [The history of databases at Netflix: From Cassandra to CockroachDB](https://www.cockroachlabs.com/blog/netflix-at-cockroachdb/) — Cockroach Labs

---

## Meta

- **Ledger DB: Not publicly disclosed** — Meta Pay (formerly Facebook Pay) exists as a payment platform, but Meta has not disclosed the database architecture behind their financial systems
- Earlier blockchain initiative (Libra/Diem) used custom distributed database but was shut down

---

## Uber

- **LedgerStore** — custom-built immutable ledger for all financial transactions (ride payments, delivery charges, etc.) [1][2]
- Originally stored indexes in **DynamoDB**, migrated to **Docstore** (Uber's internal document DB) [3][4]
- 2 trillion+ unique indexes with zero data inconsistencies detected [1]
- Migration from DynamoDB to Docstore saved $6M/year [4]
- **Ledger DB: Confirmed — LedgerStore on Docstore** (Uber's internal DB, migrated from DynamoDB) [1][3][4]

**References:**

1. [How LedgerStore Supports Trillions of Indexes at Uber](https://www.uber.com/blog/how-ledgerstore-supports-trillions-of-indexes/) — Uber Blog
2. [Uber's Finance Computation Platform](https://www.uber.com/blog/ubers-finance-computation-platform/) — Uber Blog
3. [How Uber Migrated Financial Data from DynamoDB to Docstore](https://www.uber.com/blog/dynamodb-to-docstore-migration/) — Uber Blog
4. [Uber Migrates 1 Trillion Records from DynamoDB to LedgerStore to Save $6 Million Annually](https://www.infoq.com/news/2024/05/uber-dynamodb-ledgerstore/) — InfoQ

---

## Microsoft

- Built **Azure SQL Ledger** — blockchain-inspired tamper-evident tables built into Azure SQL Database and SQL Server [1]
- Uses Merkle tree hashing for cryptographic verification of all changes [1]
- Two table types: append-only (INSERT only) and updatable (with full history tracking) [1]
- **Internal ledger DB: Not publicly disclosed** — Azure SQL Ledger is a product they sell, but Microsoft's own internal financial ledger DB is not disclosed

**References:**

1. [Ledger Overview — SQL Server](https://learn.microsoft.com/en-us/sql/relational-databases/security/ledger/ledger-overview) — Microsoft Learn
2. [Bringing the power of blockchain to Azure SQL Database](https://learn.microsoft.com/en-us/shows/data-exposed/bringing-the-power-of-blockchain-to-azure-sql-database-and-sql-server-with-ledger-data-exposed) — Microsoft Learn

---

## Summary: What's Actually Confirmed for Ledger DBs

| Company | Ledger DB Confirmed? | Database |
|---|---|---|
| Stripe | Partially | Likely not RDB — described as immutable event log, probably DocDB/MongoDB |
| PayPal | No | Undisclosed (migrating to Google Cloud/AlloyDB) |
| Amazon | **Yes** | QLDB (internal version) + DynamoDB for sub-ledgers |
| eBay | No | Undisclosed (Oracle + Cassandra for general data) |
| Walmart | No | Undisclosed |
| JPMorgan | No | Uses DB2 on mainframes but not confirmed for ledger specifically |
| Goldman Sachs | No | Undisclosed |
| OpenAI | Partially | Atomic DB transactions for credits, but specific DB not named (likely PostgreSQL) |
| Google | No | Spanner for Google Ads, but internal financial ledger not disclosed |
| Apple | No | FoundationDB for transactional workloads, but ledger specifically not disclosed |
| Netflix | **Yes** | MySQL for billing/GL, CockroachDB for multi-region workloads |
| Meta | No | Undisclosed |
| Uber | **Yes** | LedgerStore on Docstore (custom internal DB, migrated from DynamoDB) |
| Microsoft | No | Sells Azure SQL Ledger, but internal ledger DB not disclosed |

## Key Takeaway

Almost everyone builds custom ledger logic on top of battle-tested databases. **PostgreSQL** is the most common choice for new systems (OpenAI, PayPal's migration target, AWS's recommended QLDB replacement). The trend is clearly moving away from Oracle toward PostgreSQL-compatible managed services. Most companies treat their ledger implementation details as proprietary.

Companies that have fully disclosed their ledger databases: **Amazon** (QLDB), **Netflix** (MySQL), and **Uber** (LedgerStore/Docstore). Uber stands out as the most transparent — their engineering blog details the full architecture, migration, and cost savings of their ledger system.
