<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Architecture

Sluice stands between your clients and your data sources. For each query it checks your policies, rewrites the SQL so that only allowed rows and columns come back, and writes an audit record.

## Component map

```mermaid
flowchart LR
    C[AI agent / BI tool] --> REST["REST (:8080)"]
    C --> MCP["MCP (stdio / streamable HTTP)"]
    OP[Operator] --> ADM["Admin (:9091)"]
    REST --> ID[identity]
    MCP --> ID
    ID --> QS[queryservice]
    QS --> P[parser]
    QS --> PE[policy engine]
    QS --> RW[rewriter]
    QS --> EX[executor]
    QS --> AU[audit]
    EX --> DD[("DuckDB (embedded, read-only)")]
    DD --> S1[(Postgres)]
    DD --> S2[(MySQL)]
    DD --> S3[(SQLite)]
    DD --> S4[(S3)]
    DD --> S5[(DuckDB file)]
    DD --> S6[(MotherDuck)]
    DD --> S7[(HTTP)]
```

- REST and MCP use the same pipeline, so both give the same decision. See [Request lifecycle](request-lifecycle.md).
- The engine decides on each query. See [Policy engines](policy-engines.md).
- An embedded DuckDB runs the rewritten SQL. Each data source is a catalog in it, so one query can join tables from several [data sources](../operations/data-sources.md).

## Rules you can rely on

- **Default deny.** Sluice denies a query unless a policy allows every table that the query reads. An empty policy directory denies every query.
- **Audit before rows.** By default Sluice queues the audit record before it sends the first row. If it cannot, the query fails with `ERR_AUDIT_UNAVAILABLE`. See [Audit log](../security/audit.md).
- **No writes.** Sluice runs `SELECT` and `EXPLAIN`. It refuses writes, DDL, `SET` and `SHOW`. See [Statements Sluice runs](request-lifecycle.md#statements-sluice-runs).
- **Bound parameters.** Row filter values reach DuckDB as bound parameters, not as SQL text. A value from a policy cannot change the statement.
- **Reload without restart.** Sluice loads changed policies after a file change, a `SIGHUP` or `POST /admin/reload`. If the new policies have an error, the old ones stay active. See [Reload](../operations/hot-reload.md).
