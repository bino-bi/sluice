# Phase 0 inventory

Commit: 6f7b99f (origin/main). Date: 2026-10-12.

## Baseline checks

| Check | Result |
|---|---|
| `mkdocs build --strict` | passes |
| `scripts/check-docs-links.sh` | passes, 227 links in 53 files |
| `make docs-check` (old meaning: generator drift check for 4 reference pages) | passes |
| `sluice policy test examples/policies` | 4 passed, 0 failed |
| `sluice policy validate examples/<name>/policies.d` | OK for all 7 example directories |
| End-to-end runs of `examples/*/docker-compose.yaml` | not run, no test drives them and Docker is not usable in this session |

## Findings

- Nothing fails at baseline.
- No CI workflow calls `make docs-check`, `scripts/check-docs-links.sh`, `scripts/check-generated-docs.sh` or `sluice policy test`. The Makefile comment says the drift check is wired into `docs-deploy.yaml`. It is not. `docs-deploy.yaml` runs `make docs-generate` and then `mkdocs build --strict`, and only on pushes to `main`.
- `tests/e2e/` holds only a README. No end-to-end test of the examples exists.
- Only `examples/policies/` has policy tests. The other 7 example directories have none.
- `examples/approval-workflow/` is not runnable alone: no server config, no data, and `run.sh` asks for manual input.
- No doc page uses `pymdownx.snippets` today. All 195 code blocks are pasted by hand.
- No `CLAUDE.md` or `AGENTS.md` exists in the repo.

## Prose words per section (baseline)

Counted with `scripts/docs-wordcount.py`. Fenced code blocks and HTML comments are not counted. Tables are counted.

| Section | Pages | Prose words |
|---|---:|---:|
| architecture | 4 | 4063 |
| compliance | 4 | 1443 |
| cookbook | 7 | 2670 |
| getting-started | 4 | 1728 |
| home | 1 | 328 |
| operations | 6 | 3380 |
| policies | 13 | 9887 |
| reference | 9 | 4945 |
| security | 5 | 3390 |
| **total** | 53 | 31834 |

Target after rewrite: at most 19100 words (40% less).
