CREATE TABLE IF NOT EXISTS deliveries (
    id TEXT PRIMARY KEY,
    channel TEXT NOT NULL,
    received_at TEXT NOT NULL,
    status TEXT NOT NULL CHECK (
        status IN ('pending', 'processing', 'retrying', 'delivered', 'dead_letter')
    ),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TEXT,
    response_status INTEGER,
    last_error TEXT NOT NULL DEFAULT '',
    payload_hash TEXT NOT NULL,
    payload BLOB NOT NULL,
    content_type TEXT NOT NULL DEFAULT 'application/octet-stream',
    idempotency_key TEXT,
    updated_at TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_deliveries_channel_idempotency
ON deliveries(channel, idempotency_key)
WHERE idempotency_key IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_deliveries_due
ON deliveries(status, next_attempt_at);

CREATE INDEX IF NOT EXISTS idx_deliveries_received
ON deliveries(received_at DESC);
