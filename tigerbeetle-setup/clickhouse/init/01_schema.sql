-- =============================================================================
-- TigerBeetle OLAP schema for ClickHouse
--
-- Pipeline:
--   TigerBeetle → tigerbeetle amqp → LavinMQ → ClickHouse (RabbitMQ engine)
--
-- CDC JSON shape (from tigerbeetle amqp):
-- {
--   "timestamp": "1234...",  -- nanoseconds, always a string
--   "type": "single_phase",  -- event type
--   "ledger": 1,             -- top-level number
--   "transfer": {
--     "id": "123...",        -- UInt128 as decimal string (may be large)
--     "amount": 500,         -- number (may be large)
--     "pending_id": 0, "code": 1, "flags": 0, ...
--   },
--   "debit_account":  { "id": 9000010, "debits_posted": 500, ... },
--   "credit_account": { "id": 9000011, "credits_posted": 500, ... }
-- }
-- Note: UInt128 fields may appear as a decimal string (large value) or
--       as a JSON number (small/zero value); parsing handles both.
-- =============================================================================

CREATE DATABASE IF NOT EXISTS tigerbeetle;

-- ---------------------------------------------------------------------------
-- 1. Raw ingest table — RabbitMQ engine (queue consumer)
--    Binds to the 'tigerbeetle' fanout exchange on LavinMQ.
--    Rows here are transient; the MV below drains them into transfers.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS tigerbeetle.tb_events_queue
(
    raw String
)
ENGINE = RabbitMQ
SETTINGS
    rabbitmq_host_port      = 'lavinmq:5672',
    rabbitmq_exchange_name  = 'tigerbeetle',
    rabbitmq_exchange_type  = 'fanout',
    rabbitmq_format         = 'JSONAsString',
    rabbitmq_num_consumers  = 2,
    rabbitmq_vhost          = '/',
    rabbitmq_username       = 'guest',
    rabbitmq_password       = 'guest';

-- ---------------------------------------------------------------------------
-- 2. Persistent storage — MergeTree
--    Partitioned by month, ordered for fast ledger/account/time queries.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS tigerbeetle.transfers
(
    -- Event metadata
    event_type          LowCardinality(String),
    timestamp_ns        UInt64,
    event_time          DateTime64(9) MATERIALIZED fromUnixTimestamp64Nano(toInt64(timestamp_ns)),

    -- Transfer fields
    transfer_id         UInt128,
    debit_account_id    UInt128,
    credit_account_id   UInt128,
    amount              UInt128,
    ledger              UInt32,
    code                UInt16,
    flags               UInt16,
    pending_id          UInt128,
    user_data_128       UInt128,
    user_data_64        UInt64,
    user_data_32        UInt32,
    timeout             UInt32,

    -- Debit account balance snapshot at event time
    debit_credits_pending   UInt128,
    debit_credits_posted    UInt128,
    debit_debits_pending    UInt128,
    debit_debits_posted     UInt128,

    -- Credit account balance snapshot at event time
    credit_credits_pending  UInt128,
    credit_credits_posted   UInt128,
    credit_debits_pending   UInt128,
    credit_debits_posted    UInt128
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(event_time)
ORDER BY (ledger, event_time, transfer_id)
TTL toDateTime(event_time) + INTERVAL 7 YEAR
SETTINGS index_granularity = 8192;

-- ---------------------------------------------------------------------------
-- Helper macro: parse a JSON field that may be a decimal string OR a number.
-- Strategy: if JSONExtractString returns non-empty, use it (large string value);
--           otherwise fall through to JSONExtractUInt (small numeric value).
-- ---------------------------------------------------------------------------

-- ---------------------------------------------------------------------------
-- 3. Materialized View — moves rows from queue into persistent table
-- ---------------------------------------------------------------------------
CREATE MATERIALIZED VIEW IF NOT EXISTS tigerbeetle.tb_events_mv
TO tigerbeetle.transfers
AS
SELECT
    -- top-level fields
    JSONExtractString(raw, 'type')                                          AS event_type,
    toUInt64OrZero(JSONExtractString(raw, 'timestamp'))                     AS timestamp_ns,
    toUInt32OrZero(toString(JSONExtractUInt(raw, 'ledger')))                AS ledger,

    -- transfer.id is always a large decimal string
    toUInt128OrZero(JSONExtractString(raw, 'transfer', 'id'))               AS transfer_id,

    -- account IDs: small custom IDs are JSON numbers; ULID-based IDs are strings
    if(JSONExtractString(raw, 'debit_account', 'id') != '',
        toUInt128OrZero(JSONExtractString(raw, 'debit_account', 'id')),
        toUInt128OrZero(toString(JSONExtractUInt(raw, 'debit_account', 'id'))))
                                                                            AS debit_account_id,
    if(JSONExtractString(raw, 'credit_account', 'id') != '',
        toUInt128OrZero(JSONExtractString(raw, 'credit_account', 'id')),
        toUInt128OrZero(toString(JSONExtractUInt(raw, 'credit_account', 'id'))))
                                                                            AS credit_account_id,

    -- transfer numeric fields (always numbers in CDC output for typical values)
    toUInt128OrZero(toString(JSONExtractUInt(raw, 'transfer', 'amount')))   AS amount,
    toUInt16OrZero(toString(JSONExtractUInt(raw, 'transfer', 'code')))      AS code,
    toUInt16OrZero(toString(JSONExtractUInt(raw, 'transfer', 'flags')))     AS flags,
    toUInt32OrZero(toString(JSONExtractUInt(raw, 'transfer', 'timeout')))   AS timeout,

    -- pending_id and user_data_128 may be string or number
    if(JSONExtractString(raw, 'transfer', 'pending_id') != '',
        toUInt128OrZero(JSONExtractString(raw, 'transfer', 'pending_id')),
        toUInt128OrZero(toString(JSONExtractUInt(raw, 'transfer', 'pending_id'))))
                                                                            AS pending_id,
    if(JSONExtractString(raw, 'transfer', 'user_data_128') != '',
        toUInt128OrZero(JSONExtractString(raw, 'transfer', 'user_data_128')),
        toUInt128OrZero(toString(JSONExtractUInt(raw, 'transfer', 'user_data_128'))))
                                                                            AS user_data_128,
    toUInt64OrZero(toString(JSONExtractUInt(raw, 'transfer', 'user_data_64')))  AS user_data_64,
    toUInt32OrZero(toString(JSONExtractUInt(raw, 'transfer', 'user_data_32')))  AS user_data_32,

    -- debit account balance snapshot
    toUInt128OrZero(toString(JSONExtractUInt(raw, 'debit_account', 'credits_pending'))) AS debit_credits_pending,
    toUInt128OrZero(toString(JSONExtractUInt(raw, 'debit_account', 'credits_posted')))  AS debit_credits_posted,
    toUInt128OrZero(toString(JSONExtractUInt(raw, 'debit_account', 'debits_pending')))  AS debit_debits_pending,
    toUInt128OrZero(toString(JSONExtractUInt(raw, 'debit_account', 'debits_posted')))   AS debit_debits_posted,

    -- credit account balance snapshot
    toUInt128OrZero(toString(JSONExtractUInt(raw, 'credit_account', 'credits_pending'))) AS credit_credits_pending,
    toUInt128OrZero(toString(JSONExtractUInt(raw, 'credit_account', 'credits_posted')))  AS credit_credits_posted,
    toUInt128OrZero(toString(JSONExtractUInt(raw, 'credit_account', 'debits_pending')))  AS credit_debits_pending,
    toUInt128OrZero(toString(JSONExtractUInt(raw, 'credit_account', 'debits_posted')))   AS credit_debits_posted
FROM tigerbeetle.tb_events_queue;

-- ---------------------------------------------------------------------------
-- 4. OLAP views
-- ---------------------------------------------------------------------------

-- Daily transfer volume and amount by ledger
CREATE VIEW IF NOT EXISTS tigerbeetle.v_daily_volume AS
SELECT
    toDate(event_time)  AS day,
    ledger,
    count()             AS transfer_count,
    sum(amount)         AS total_amount
FROM tigerbeetle.transfers
WHERE event_type IN ('single_phase', 'two_phase_posted')
GROUP BY day, ledger
ORDER BY day DESC, ledger;

-- Latest net position per account (debits_posted - credits_posted at last event)
CREATE VIEW IF NOT EXISTS tigerbeetle.v_account_balances AS
SELECT
    debit_account_id    AS account_id,
    ledger,
    argMax(debit_debits_posted,  event_time)
        - argMax(debit_credits_posted, event_time) AS net_position,
    max(event_time)     AS last_seen
FROM tigerbeetle.transfers
WHERE event_type IN ('single_phase', 'two_phase_posted')
GROUP BY account_id, ledger;
