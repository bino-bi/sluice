# Contributing to sluice

Thank you for your interest in contributing. This document covers the ground rules.

## Developer Certificate of Origin (DCO)

All contributions must be signed off per the [Developer Certificate of Origin 1.1](https://developercertificate.org/). Every commit message must include a `Signed-off-by:` trailer matching the author:

```
Signed-off-by: Your Name <your.email@example.com>
```

Use `git commit -s` to add the trailer automatically.

## Licensing of contributions

By submitting a contribution, you agree that your work is licensed under the project's existing terms:

- Changes to files under `pkg/` or `sdk/` → **Apache-2.0**
- Changes elsewhere → **AGPL-3.0-or-later**

Every new source file must begin with the correct SPDX header. `scripts/check-spdx.sh` enforces this in CI.

## Development workflow

```bash
make fmt        # gofmt + goimports
make vet        # go vet
make lint       # golangci-lint run + SPDX check
make test       # go test -race -short ./...
make all        # everything above, then build
```

CI runs `make lint test` on Linux; the release pipeline cross-compiles.

## Coding conventions

- Exported Go identifiers: CamelCase. Packages: lowercase single word.
- YAML fields: lowerCamelCase. Env vars: `SLUICE_*`. CLI flags: kebab-case.
- Errors: sentinel per package; client-facing responses funnel through `pkg/errors.APIError`.
- `log/slog` for all structured logging. `fmt.Print*` is forbidden outside `cmd/sluice/` (enforced by `forbidigo`).
- Time: UTC, RFC 3339 for audit; `time.Now().UTC()` everywhere.
- Layering: `pkg/**` must never import `internal/**`. `cmd/sluice` is the only composition root. Enforced by `depguard`.

## Pull requests

- Small, focused diffs. One logical change per PR.
- Tests alongside code (`*_test.go`). Integration tests (deferred to later slice) will live under `tests/integration/`.
- CI must be green before review.

## Security

Do not file security issues as public GitHub issues. See `SECURITY.md` (landing in a later slice) for the coordinated disclosure process.
