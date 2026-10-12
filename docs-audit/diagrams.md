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

## Label changes

None applied yet.

Found by the audit, waiting for a decision:

| Diagram | Finding | Evidence |
|---|---|---|
| 2, component map | Node label `S3 Parquet`: the type is `s3` and reads parquet, csv, json and xlsx | `pkg/apitypes/datasource.go:58-73` |
| 2, component map | Edge `Admin --> identity`: the admin listener checks a static bearer token and does not use the identity layer. This is an edge, not a label | `internal/transport/admin/auth.go:16-31` |
| 2, component map | The `http` datasource type has no node. This is a missing node, not a label | `pkg/apitypes/datasource.go:62` |

Diagrams 1, 3, 4 and 5: no label is proven wrong.
