# sluice

A SQL access-policy gateway that sits between clients (BI tools, agents, SDKs) and your databases. Sluice parses inbound SQL, applies declarative policies (row filters, column masks, rejections, rewrites), and proxies the rewritten query to the underlying data source — all while emitting a tamper-evident audit trail.

> **Status:** Pre-alpha. The first implementation slice is under construction — public API contracts and foundational infrastructure only. The query path is not yet wired up.

## Layout

```
cmd/sluice/        Composition root & CLI
pkg/               Public API (Apache-2.0)
  errors/          Stable client-facing error catalog
  apitypes/        Policy DSL types
  mask/            Column-mask Provider interface & built-ins
  datasource/      DataSource driver interface
internal/          Private packages (AGPL-3.0-or-later)
  version/         Build identity (ldflags)
  telemetry/       slog, Prometheus, OTel plumbing
  secrets/         secret:// resolution
  config/          Server + policy loading
```

## Licensing

- Everything under `pkg/` and `sdk/` is **Apache-2.0**.
- Everything else is **AGPL-3.0-or-later**.

Every source file carries an SPDX header. See `LICENSE`, `LICENSE-APACHE`, and `NOTICE`.

## Development

```bash
make build      # build ./bin/sluice
make test       # go test -race -short ./...
make lint       # golangci-lint + SPDX check
make all        # fmt → vet → lint → test → build
```

## Contributing

See `CONTRIBUTING.md`. Contributions are accepted under DCO (`Signed-off-by:` trailer).
