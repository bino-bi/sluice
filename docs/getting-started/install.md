<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Install

## Binary releases

Pre-built, cosign-signed binaries for Linux (amd64/arm64), macOS
(amd64/arm64), and Windows (amd64) are published on every
[GitHub release](https://github.com/bino-bi/sluice/releases).

```bash
# Linux amd64 example
curl -sSL -o sluice.tar.gz \
  "https://github.com/bino-bi/sluice/releases/download/v0.1.0/sluice_0.1.0_linux_amd64.tar.gz"
tar -xzf sluice.tar.gz
./sluice version
```

Verify with cosign:

```bash
cosign verify-blob \
  --certificate checksums.txt.pem \
  --signature checksums.txt.sig \
  --certificate-identity-regexp 'https://github.com/bino-bi/sluice/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
```

## Docker

Multi-arch images are published to `ghcr.io/bino-bi/sluice`:

```bash
docker pull ghcr.io/bino-bi/sluice:latest
docker run --rm ghcr.io/bino-bi/sluice:latest version
```

Images are distroless (non-root, uid 65532) and cosign-signed.

## Build from source

Requires Go 1.25 or later and a C toolchain (pg_query_go and go-duckdb
are both cgo).

```bash
git clone https://github.com/bino-bi/sluice.git
cd sluice
make build
./bin/sluice version
```

Run `make all` to go through format → vet → lint → test → build in one
shot.

## Platform support

| Platform       | Tier | Notes                                                 |
| -------------- | ---- | ----------------------------------------------------- |
| Linux amd64    | 1    | Primary target; nightly integration lane.             |
| Linux arm64    | 1    | Multi-arch Docker image + standalone binary.          |
| macOS amd64    | 1    | Developer workstation.                                |
| macOS arm64    | 1    | Apple silicon.                                        |
| Windows amd64  | 2    | Build-verified in CI; integration tests skipped.      |
