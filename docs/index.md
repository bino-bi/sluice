<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Sluice

Sluice is a policy-enforcing SQL gateway that sits between AI agents, BI tools, and your databases. Every statement is parsed with the real PostgreSQL grammar (pg_query), checked against declarative default-deny YAML policies, rewritten in flight — row filters, column masks, LIMIT and sampling clamps — and executed read-only through embedded DuckDB against attached catalogs. Each request, allowed or denied, lands in a hash-chained audit log you can verify offline.

```
client (REST / MCP) ─▶ identify ─▶ parse ─▶ policy ─▶ rewrite ─▶ execute (DuckDB, read-only) ─▶ audit ─▶ rows
```

## One policy, applied on the wire

A `RowFilterPolicy` scopes every matching query to the caller's tenant:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: RowFilterPolicy
metadata:
  name: filter-tenant
  priority: 80
spec:
  match:
    any:
      - resources:
          tables: ["shop.main.customers", "shop.main.orders"]
  combine: restrictive
  filter:
    predicate:
      column: tenant_id
      op: Equals
      value: "{{ subject.tenantId }}"
```

The client sends:

```sql
SELECT id, email FROM shop.main.customers
```

Sluice executes:

```sql
SELECT id, email FROM (SELECT * FROM shop.main.customers WHERE tenant_id = $1) customers
```

with `$1` bound to the caller's `tenantId` claim — a positional parameter, never string concatenation.

## Features

- **11 policy kinds** — `SqlAccessPolicy`, `RowFilterPolicy`, `ColumnMaskPolicy`, `QueryRejectPolicy`, `QueryRewritePolicy`, `ApprovalPolicy`, `DataSource`, `SubjectBinding`, `AuditSink`, `DataClassification`, `RelationshipPolicy`.
- **Mask catalog** — SQL-stage `null`, `constant`, `partial`, `hash`, `regex`, `truncate`; post-query HMAC hashing, format-preserving encryption (FF1), `jitter`, deterministic `fake` data.
- **CEL** — policy conditions, reject rules, and a safe row-filter expression subset.
- **Approval workflow** — park sensitive queries for a human decision, delivered via webhooks; approvals mint single-use grants.
- **Pluggable engines** — embedded OPA (Rego) and OpenFGA-backed ReBAC compose with the YAML engine.
- **Rate limits and budgets** — per-subject token buckets plus daily CPU-seconds/rows quotas.
- **Hot reload** — file watcher, SIGHUP, or `POST /admin/reload`; invalid snapshots never replace a good one.
- **Policy test harness** — `sluice policy test` runs fixture suites in CI.
- **Tamper-evident audit** — hash-chained JSONL, verified with `sluice audit verify`.
- **MCP server** — 9 tools for agents, over stdio or Streamable HTTP.
- **6 datasource drivers** — Postgres, MySQL, SQLite, S3 Parquet, DuckDB file, MotherDuck.

## Where to start

| You are | Start here |
| --- | --- |
| An operator standing up a gateway | [Getting started](getting-started/index.md) |
| A policy author | [Policies](policies/index.md) |
| An agent builder wiring up MCP | [MCP tools reference](reference/mcp.md) |
| A security reviewer | [Security model](architecture/security-model.md) |

!!! warning "Project status: alpha"
    Sluice is pre-`v0.1.0`. The full query path works end to end, but there are no published release binaries or container images yet — you build from source or use the docker-compose examples. Interfaces may change between commits.
