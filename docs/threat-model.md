# Threat Model

## Assets

- Channel HMAC secrets
- Webhook payloads stored in SQLite
- Delivery metadata and failure details
- Availability of the relay and target services

## Trust boundaries

Internet clients cross the HTTP boundary when submitting events. Target URLs
cross an outbound network boundary. The JSON configuration is trusted operator
input, while DNS responses and target HTTP responses are untrusted.

## Key threats and controls

| Threat | Control | Residual risk |
| --- | --- | --- |
| Forged webhook | Constant-time HMAC SHA-256 verification | A leaked channel secret permits forgery |
| Replay or duplicate | Unique channel and `Idempotency-Key` pair | Senders must supply stable keys |
| Memory exhaustion | 1 MiB body and header limits, server timeouts | Request-rate limiting belongs at the proxy |
| SSRF | Scheme, userinfo, DNS, private, loopback, and link-local checks; DNS is rechecked on each dial | Operators can explicitly disable address blocking for local development |
| Secret leakage | Secrets come from environment variables; sensitive headers are redacted | Payload content is persisted and should be treated as sensitive |
| Target outage | Durable queue, bounded exponential retry, jitter, and dead-letter state | SQLite supports one relay process, not horizontal workers |
| Abrupt shutdown | In-flight records are recovered at startup | At-least-once delivery can produce duplicates |

## Out of scope

The service does not provide TLS termination, user authentication for delivery
inspection, distributed coordination, payload encryption, or rate limiting.
Deploy it behind an authenticated, rate-limited reverse proxy on a trusted
network.
