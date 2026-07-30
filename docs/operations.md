# Operations Guide

## Delivery lifecycle

`pending` records are eligible immediately. A worker changes one due record to
`processing` and increments its attempt count in a transaction. A 2xx target
response moves it to `delivered`. Network errors, timeouts, and non-2xx
responses move it to `retrying` with a future `nextAttemptAt`. Exhausting
`maxAttempts` moves it to `dead_letter`.

At startup, any `processing` records left by an interrupted process are changed
to `retrying`. This is an at-least-once design: a target can receive a duplicate
if it processed a request but the relay stopped before recording success.
Targets should deduplicate `X-Relay-Delivery-ID` or `Idempotency-Key`.

## Dead-letter recovery

1. Inspect the record with `GET /deliveries/{id}`.
2. Correct the target or its dependency.
3. Submit `POST /deliveries/{id}/retry`.
4. Confirm the delivery reaches `delivered` and review `/metrics`.

Only `dead_letter` records can be manually retried. The retry endpoint resets
the attempt count to give the corrected target a fresh attempt cycle.

## Backup and restore

Stop the relay before copying the SQLite database so the database, WAL, and SHM
files cannot diverge:

```bash
docker compose stop relay
docker run --rm -v reliable-webhook-relay_relay_data:/data \
  -v "$PWD/backups:/backup" alpine:3.22.1 \
  cp /data/relay.db /backup/relay.db
docker compose start relay
```

To restore, stop the relay, replace `/data/relay.db` from a trusted backup, and
start it again. Test restores regularly.

## Useful checks

```bash
curl --fail http://localhost:8080/health
curl --fail http://localhost:8080/metrics
curl --fail 'http://localhost:8080/deliveries?status=dead_letter&limit=20'
docker compose logs --since=10m relay
```

Metrics are process-local counters and reset on restart. Delivery state remains
durable in SQLite.
