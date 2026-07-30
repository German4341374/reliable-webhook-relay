## Summary

Describe the behavior changed and why.

## Verification

- [ ] `gofmt -l .` prints no files
- [ ] `go vet ./...`
- [ ] `go test -race ./...`
- [ ] Documentation is updated
- [ ] No secrets or sensitive payloads are included

## Security impact

Describe changes to signature handling, SSRF controls, persistence, or permissions. Write `None` if not applicable.
