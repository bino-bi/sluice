# Diagram inventory

Taken at commit 6f7b99f, before any rewrite. Every diagram listed here must still be present and render after the rewrite.

| # | File | Type | First line | Line |
|---|---|---|---|---:|
| 1 | `docs/index.md` | ASCII pipeline | `client (REST / MCP) ─▶ identify ─▶ parse ─▶ policy ─▶ rewrite ─▶ execute (DuckDB, read-only) ─▶ audit ─▶ rows` | 8 |
| 2 | `docs/architecture/index.md` | Mermaid | `flowchart LR` (component map) | 18 |
| 3 | `docs/architecture/request-lifecycle.md` | Mermaid | `sequenceDiagram` (request lifecycle) | 10 |
| 4 | `docs/policies/approvals.md` | Mermaid | `sequenceDiagram` (approval flow) | 13 |
| 5 | `docs/security/threat-model.md` | Mermaid | `flowchart LR` (trust boundaries) | 36 |

The original blocks are kept byte for byte in `docs-audit/diagrams/`. The final check compares each block in `docs/` with its copy there.

## Changes

The maintainer approved these three changes to diagram 2 (component map) on 2026-10-12. The copy in `diagrams/architecture-index.mmd` holds the new version.

| Change | Reason | Evidence |
|---|---|---|
| Node label `S3 Parquet` is now `S3` | The type is `s3`. It reads parquet, csv, json and xlsx | `pkg/apitypes/datasource.go:58-73` |
| New node `HTTP` | The `http` data source type had no node | `pkg/apitypes/datasource.go:62` |
| Edge `Admin --> identity` removed | The admin listener checks a static bearer token. It does not use the identity layer | `internal/transport/admin/auth.go:16-31` |

Diagrams 1, 3, 4 and 5 are unchanged. No label in them is proven wrong.
