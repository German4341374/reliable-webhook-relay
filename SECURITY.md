# Security Policy

## Reporting

Use GitHub private vulnerability reporting for security issues. Do not open a
public issue containing a secret, webhook payload, exploitable target URL, or
proof of concept against a live service.

## Supported version

The latest commit on `main` is supported. This repository does not
promise long-term maintenance for older commits.

## Deployment baseline

- Keep `RELAY_ALLOW_PRIVATE_TARGETS` unset in production.
- Store channel secrets outside the JSON configuration.
- Terminate TLS at a trusted reverse proxy and authenticate administrative
  delivery endpoints at the network boundary.
- Restrict access to the SQLite database and back it up securely.
- Rotate channel secrets and review dead-letter deliveries.
- Pin and scan the container image before deployment.
