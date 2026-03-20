-- +goose Up

-- Append-only event log for stress test metrics
CREATE TABLE stress_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id VARCHAR(64) NOT NULL,           -- groups events by stress test run
    event_type VARCHAR(32) NOT NULL,       -- DEPOSIT, WITHDRAWAL, TRANSFER
    account_id UUID NOT NULL,
    success BOOLEAN NOT NULL,
    latency_ms DOUBLE PRECISION NOT NULL,  -- server-side latency
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_stress_events_run ON stress_events(run_id, created_at);
CREATE INDEX idx_stress_events_run_type ON stress_events(run_id, event_type);

-- +goose Down
DROP TABLE IF EXISTS stress_events;
