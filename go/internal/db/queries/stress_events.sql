-- name: InsertStressEvent :exec
INSERT INTO stress_events (run_id, event_type, account_id, success, latency_ms, error_message)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetStressRunSummary :many
SELECT
    event_type,
    COUNT(*) AS total_count,
    COUNT(*) FILTER (WHERE success = true) AS success_count,
    COUNT(*) FILTER (WHERE success = false) AS error_count,
    AVG(latency_ms) AS avg_latency_ms,
    percentile_cont(0.50) WITHIN GROUP (ORDER BY latency_ms) AS p50_latency_ms,
    percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms) AS p95_latency_ms,
    percentile_cont(0.99) WITHIN GROUP (ORDER BY latency_ms) AS p99_latency_ms,
    MAX(latency_ms) AS max_latency_ms,
    MIN(created_at) AS first_event_at,
    MAX(created_at) AS last_event_at
FROM stress_events
WHERE run_id = $1
GROUP BY event_type;

-- name: GetStressRunQPS :many
SELECT
    date_trunc('second', created_at) AS second,
    COUNT(*) AS request_count,
    COUNT(*) FILTER (WHERE success = true) AS success_count
FROM stress_events
WHERE run_id = $1
GROUP BY date_trunc('second', created_at)
ORDER BY second;

-- name: ListStressRuns :many
SELECT
    run_id,
    COUNT(*) AS total_events,
    MIN(created_at) AS started_at,
    MAX(created_at) AS ended_at
FROM stress_events
GROUP BY run_id
ORDER BY MIN(created_at) DESC
LIMIT $1;
