# Contributing

## Development workflow

1. Fork the repository and create a focused branch.
2. Install Go 1.26.6 and run `make setup`.
3. Add tests for behavior changes.
4. Run `make lint`, `make test`, and `make build`.
5. Open a pull request using the repository template.

Use [Conventional Commits](https://www.conventionalcommits.org/), for example
`feat(worker): add bounded retry delay` or `fix(security): reject IPv6 loopback targets`.

Keep changes small, never commit secrets or real webhook payloads, and document
security-relevant trade-offs.
