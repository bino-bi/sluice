<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Deployment

Sluice ships as a single self-contained binary plus a multi-arch Docker
image. In-process DuckDB means one Sluice process is one SQL plane — no
external database or control channel required for the gateway itself.

## Topology

```
            ┌────────────────────┐
  clients ──┤   Sluice (REST)    ├── Postgres ─┐
            │    Sluice (MCP)    │             │
            │   Sluice (admin)   ├── S3 Parquet ├── attached via DuckDB
            └──┬────────┬────────┘             │
               │ audit  │ metrics   ... MySQL ─┘
               v        v
            files   Prometheus
```

A single replica suffices for a team-sized workload. Horizontal scaling
requires:

- A shared audit sink (S3 Object Lock lands in v1).
- A shared JWKS / policy distribution channel (git + fsnotify works
  today; a real control plane lands in v1).

## Docker

```bash
docker run -d --name sluice \
  -p 8080:8080 -p 9090:9090 \
  -v $(pwd)/server.yaml:/etc/sluice/server.yaml:ro \
  -v $(pwd)/policies.d:/etc/sluice/policies.d:ro \
  -v sluice-audit:/var/lib/sluice/audit \
  ghcr.io/bino-bi/sluice:latest serve \
    --config /etc/sluice/server.yaml \
    --policies-dir /etc/sluice/policies.d
```

## Kubernetes

A Helm chart lands in the [`examples/` directory](https://github.com/bino-bi/sluice/tree/main/examples)
for v0.2. Until then, mount the config + policies as ConfigMaps, the
audit directory as a PersistentVolumeClaim, and inject secrets via
`SLUICE_*` env vars from a Secret.

## Non-functional targets (MVP)

- Binary size: < 100 MiB.
- Cold start: < 2 s on a typical cloud VM.
- Graceful shutdown drain: 10 s (`serve` ctx deadline).
- Audit throughput: ≥ 10 k records/s (benched at 220 k on M1 Pro).
