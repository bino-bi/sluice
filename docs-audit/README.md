# Docs audit

Working files for the docs simplification. This folder is not published.

| File | Content |
|---|---|
| `phase0-inventory.md` | Baseline checks and prose word counts before the rewrite |
| `diagrams.md`, `diagrams/` | Every diagram that must stay, with a byte copy of each |
| `getting-started.md` | Audit of Home and Getting started |
| `architecture.md`, `policies.md`, `operations.md`, `security.md`, `reference.md`, `cookbook.md` | Audit of each section |

The Compliance section was not audited because it is deleted. `security.md` lists the two facts from it that are documented nowhere else.

## How the audit was made

One agent per section read every page and checked each claim against the code at commit 6f7b99f. Each agent also ran a local server built from that commit. Rows marked "ran" or "run" were executed. Other rows were checked by reading the code.

## Classes

| Class | Meaning |
|---|---|
| `implemented (file:line)` | The code does this and a user can reach it |
| `not implemented` | Planned, partial, not wired up, or not in the code |
| `mismatch` | The feature exists but the page states it wrong. The code wins |
| `internal detail` | True, but a user cannot act on it |
| `fluff` | Marketing, filler, counts that go stale |
| `duplicate` | Repeated from another page. The row names the owner page |

Code blocks are `tested` (an automated test runs the same content), `untested` or `broken`.

## Totals

About 1300 claim rows: 869 implemented, 156 mismatch, 62 not implemented, 59 internal detail, 54 fluff, 95 duplicate. 21 broken code blocks.
