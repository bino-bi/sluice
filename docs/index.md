<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Sluice

**Sluice** is a policy-enforcing SQL/MCP gateway that sits in front of
DuckDB-attached data sources (Postgres, MySQL, SQLite, S3 Parquet,
DuckDB file, MotherDuck). Every query flows through a single pipeline:

```
parse → identify → policy → rewrite → execute → audit
```

The result: analysts, dashboards, and LLM agents speak SQL; row filters
and column masks are applied *before* the query reaches any backend; an
append-only, hash-chained audit log records exactly what ran.

## Why Sluice?

- **Default-deny.** An empty policy set rejects every query. Access is
  granted explicitly, never assumed.
- **Defence in depth.** Rewrites happen at the AST level (pg_query);
  templated values flow through positional parameters — never
  concatenation.
- **One plane, many transports.** A single `queryservice.Service` is
  shared by REST, MCP (stdio + Streamable HTTP), and the admin port.
- **Tamper-evident audit.** SHA-256 hash-chained JSONL; `sluice audit
  verify` replays the chain at any time.
- **Open core, OSS licensed.** AGPL-3.0 for the gateway; Apache-2.0 for
  public-API packages; CC-BY-4.0 for these docs.

## Where to start

| Audience                  | Start here                                              |
| ------------------------- | ------------------------------------------------------- |
| New user, 30 min demo     | [Getting started](getting-started/index.md)             |
| Policy author             | [Concepts → Policies](concepts/policies.md)             |
| Operator / SRE            | [Operations → Deployment](operations/deployment.md)     |
| Security reviewer         | [Security → Threat model](security/threat-model.md)     |
| LLM / agent integrator    | [Reference → MCP tools](reference/mcp.md)               |

## Project status

MVP `v0.1.0` is tracking a single end-to-end round-trip through all
three transports with a SQLite catalog. See
[CHANGELOG](https://github.com/bino-bi/sluice/blob/main/CHANGELOG.md)
for the current drop and [ROADMAP](https://github.com/bino-bi/sluice/blob/main/ROADMAP.md)
for what comes next.
