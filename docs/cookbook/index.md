<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Cookbook

Each recipe is a complete setup under `examples/` that you can run.

| Recipe | What it shows | Policy kinds |
|---|---|---|
| [Multi-tenant isolation](multi-tenant.md) | One row filter separates two tenants. | `RowFilterPolicy` |
| [PII masking](pii-masking.md) | Three columns come back masked. | `ColumnMaskPolicy` |
| [Break-glass access](break-glass.md) | One group sees the real values. | `ColumnMaskPolicy` |
| [Approval workflow](approval-workflow.md) | A person must accept a query. | `ApprovalPolicy` |
| [Cross-source joins](cross-source-joins.md) | One query joins two data sources. | `ColumnMaskPolicy` |
| [MCP agents](mcp-agents.md) | An agent queries under a row limit. | `QueryRewritePolicy` |

Every recipe also has a `SqlAccessPolicy`. Without an allow, Sluice denies every query.

## Run an example

1. Build `sluice` and put it on your `PATH`. See the [Quickstart](../getting-started/index.md).
2. Start the server with `examples/<name>/serve.sh`.
3. From the example folder, run the files in `steps/` in order.

`expected/` holds the real output of each step. The recipes show it indented, with placeholders such as `<query-id>` for values that change.
