<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Sluice

Sluice is a SQL gateway that enforces policies between your clients and your databases. Clients such as AI agents and BI tools send SQL over REST or MCP. Sluice parses each statement with the PostgreSQL grammar and denies it unless your YAML policies allow it. It adds row filters and column masks, runs the statement read-only in DuckDB and writes an audit record.

```
client (REST / MCP) ─▶ identify ─▶ parse ─▶ policy ─▶ rewrite ─▶ execute (DuckDB, read-only) ─▶ audit ─▶ rows
```

## Example

This row filter limits each subject to the rows of its tenant.

```yaml
--8<-- "examples/hello-sluice/policies.d/filter-tenant.yaml:2"
```

This API key belongs to tenant `acme`.

```bash
--8<-- "examples/hello-sluice/steps/05-tenant-rows.sh"
```

The table holds five customers. Sluice returns only the two `acme` rows.

```json
--8<-- "examples/hello-sluice/expected/05-tenant-rows.txt"
```

## What you can do

- [Allow or deny tables](policies/access-control.md) per subject.
- [Filter rows](policies/row-filters.md) and [mask columns](policies/column-masks.md).
- [Reject or rewrite](policies/guardrails.md) a statement, or [hold it for approval](policies/approvals.md).
- Decide with [CEL conditions](policies/conditions.md), [OPA](policies/opa.md) or [OpenFGA](policies/rebac.md).
- Set [rate limits and budgets](policies/subjects.md).
- [Test](policies/testing.md) and [reload](operations/hot-reload.md) policies.
- [Verify the audit log](security/audit.md).
- Give AI agents [MCP tools](reference/mcp.md).
- Attach [data sources](operations/data-sources.md) such as Postgres, SQLite and S3.

## Where to start

- [Quickstart](getting-started/index.md): run this example.
- [Install](getting-started/install.md): image, archive or source build.
- [Policies](policies/index.md): write your own.
- [Cookbook](cookbook/index.md): complete setups.
- [Security](security/index.md): defaults and threat model.
