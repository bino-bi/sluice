# Docs style guide

These rules apply to every page under `docs/`. This file is not published.

The goal: a reader finds a feature, sees a working example, and understands it in under a minute. Many readers are not native English speakers.

## 1. Document only what the code does today

- Check every feature, field, flag, default, metric, error code, MCP tool and endpoint against the code.
- Keep it only if it is implemented and a user can reach it.
- Do not write about planned or partial features. Do not write "planned", "coming soon" or "not yet implemented".
- If docs and code disagree, the code wins. Fix the docs.
- Leave out internals a user cannot act on: package paths, parser edge cases, cache keys, design history.
- If a user can hit a limit, write one short line under "Limits" or in the error-code reference.
- Reference pages are tables. Generate them from code where a generator exists (`make docs-generate`).

## 2. Plain, simple English

- One idea per sentence. Aim for 8 to 15 words. The hard limit is 25 words.
- Use active voice and present tense. Talk to the reader as "you".
- Use common words. Explain a technical term once, at first use, or link to the [glossary](docs/reference/glossary.md).
- Use one term per concept everywhere. The glossary lists the terms.
- Delete every sentence that does not help the reader do something.
- No em dashes and no en dashes in prose. Write "8 to 15", not a dash range.
- No semicolon chains. No nested parentheses.
- No marketing. No feature counts such as "11 policy kinds". Counts go stale.
- Use tables for fields and options. Use lists for three or more parallel items.

Do not use these words and phrases:

> seamless, robust, powerful, leverage, ensure, comprehensive, effortless, out of the box, under the hood, on the wire, in flight, first-class, battle-tested, simply, just, easily, delve, crucial, moreover, "it's worth noting", "note that", "it is important to", "in summary"

### Terms

Use the term on the left. The [glossary](docs/reference/glossary.md) explains each one.

| Say | Do not say |
|---|---|
| Sluice, the server | the gateway, the proxy, the service |
| client | caller, consumer, requester |
| subject | principal, identity, user (for the thing policies match) |
| policy | manifest, resource, rule (a "rule" is an entry in `QueryRejectPolicy.rules`) |
| policy kind | policy type, object type |
| policy directory | policies dir, policy folder |
| data source | datasource (except in code and commands), upstream, backend |
| column mask, mask type | masking rule, mask provider, mask function |
| row filter | row-level security, RLS |
| audit log, audit record | audit trail, audit entry, audit event |
| reload | hot reload, live reload |
| rewritten SQL | final SQL, rewritten statement |
| server config | config file, `sluice.yaml` |
| deny (`ACL_DENIED`), reject (`ACL_REJECTED`) | block, forbid (as a synonym for either) |
| approval, grant | sign-off, permit |

## 3. Examples

- Every feature page starts with a working example.
- Example shape: goal (1 sentence), policy or config, command or query, real output, "What happened" (at most 3 sentences).
- Every example is a set of real files under `examples/<name>/`, with sample data.
- Pages include these files with `pymdownx.snippets` (`--8<--`). Never paste code by hand.
- A fragment is allowed only as a named snippet section of a tested file.
- Outputs come from real runs and are stored as golden files. Never write expected output by hand.
- [examples/README.md](examples/README.md) explains the layout and how to add an example.
- Include a whole file, a file from line 2 to skip its SPDX line (`file.yaml:2`), or a named section (`file.yaml:name`). Do not use other line numbers.

## 4. Diagrams

- Keep every diagram listed in `docs-audit/diagrams.md`.
- Do not delete, redraw or restyle a diagram. Change a label only if the code proves it wrong.
- You may add a diagram in the same style if it replaces a long paragraph.
- List each change to a diagram in `docs-audit/diagrams.md` and update its copy in `docs-audit/diagrams/`.

## 5. Structure

The top-level sections are fixed. Do not remove or rename them:

Home, Getting started, Architecture, Policies, Operations, Security, Reference, Cookbook.

Rules for the Security section:

- The threat model is one table with the columns `Threat | What Sluice does | What you must do`.
- List only protections that exist in the code. Say under which condition a protection holds.
- Audit log, hardening and key rotation use short steps and tested examples, not long reasoning.

### One owner per fact

Write a fact on one page. Link to that page from the others.

| Fact | Owner page |
|---|---|
| Server config keys and defaults | `reference/configuration.md` (generated) |
| All fields of every policy kind | `reference/policy-schema.md` (generated) |
| Error codes and HTTP status | `reference/error-codes.md` (generated) |
| Metrics | `reference/metrics.md` (generated) |
| CLI commands, flags, exit codes | `reference/cli.md` (generated) |
| REST fields, admin endpoints, MCP tools | `reference/rest-api.md`, `admin-api.md`, `mcp.md` |
| `metadata`, `priority`, `enforcementMode` | `policies/index.md` |
| `match`, `exclude`, wildcards | `policies/matching.md` |
| `conditions`, the `query.*` values | `policies/conditions.md`, `policies/guardrails.md` |
| Stages, statements Sluice runs, error per stage | `architecture/request-lifecycle.md` |
| Engine selection, composite rules | `architecture/policy-engines.md` |
| API keys, JWT, rate limits, budgets | `policies/subjects.md` |
| Approval flow and its config | `policies/approvals.md` |
| Secrets, config loading | `operations/server-config.md` |
| Health endpoints, ports, TLS, images | `operations/deployment.md` |
| Audit record fields, sinks, verify | `security/audit.md` |

## 6. Page template

Use this for feature pages. Reference pages and the threat model may use tables instead.

```
# <Feature name>
<1-2 sentences: what it does and when you use it.>

## Example
<tested example: goal, files, run, output, what happened>

## Fields
<table: name | type | default | meaning>

## More examples
<tested variants>

## Limits
<only if the user must know. Max 5 bullets.>

## See also
<2-4 links>
```

## 7. Check your changes

```bash
make docs-check
```

It fails when:

- an example fails, or its output differs from the golden files,
- a generated reference page is out of date (`make docs-generate` fixes it),
- a page breaks a rule from this guide (`scripts/docs-style-check.py`),
- `mkdocs build --strict` fails, for example on a broken link.

`make docs-update` rewrites the golden files from a real run. Read the diff before you commit it.

To count prose words per section, run `python3 scripts/docs-wordcount.py`.
