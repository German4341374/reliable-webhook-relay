# Repository Guidance

- Keep source code, comments, documentation, and commits in English.
- Use the standard library unless a dependency removes substantial risk.
- Never log payloads, signatures, authorization values, cookies, or secrets.
- Keep SSRF checks at configuration time and connection time.
- Preserve durable-before-delivery and at-least-once semantics.
- Add or update tests for HMAC, idempotency, retries, persistence, and API changes.
- Use Conventional Commits and run format, vet, race tests, and builds before pushing.
