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

None yet.
