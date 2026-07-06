<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Cookbook

Task-oriented recipes for common Sluice deployments. Each recipe maps 1:1 to a
runnable directory under `examples/` in the repository: the YAML you see on the
page is the configuration you run, and every recipe ends with a way to verify
the policy actually does what it claims — either with `sluice policy test` or
with two curl calls whose outputs must differ.

## Recipes

| Scenario | Policy kinds exercised | Example directory | Time |
|---|---|---|---|
| [Multi-tenant isolation](multi-tenant.md) | SqlAccessPolicy, RowFilterPolicy, SubjectBinding | `examples/multi-tenant/` | ~10 min |
| [PII masking](pii-masking.md) | SqlAccessPolicy, ColumnMaskPolicy | `examples/pii-masking/` | ~10 min |
| [Break-glass access](break-glass.md) | SqlAccessPolicy, ColumnMaskPolicy (with `exclude`), SubjectBinding | `examples/break-glass/` | ~10 min |
| [Cross-source joins](cross-source-joins.md) | DataSource, SqlAccessPolicy, QueryRejectPolicy | `examples/cross-source-join/` | ~15 min |
| [Approval workflow](approval-workflow.md) | ApprovalPolicy, SqlAccessPolicy | `examples/approval-workflow/` | ~15 min |
| [MCP agents](mcp-agents.md) | SqlAccessPolicy, SubjectBinding (rate limit + budget), QueryRewritePolicy | `examples/mcp-agent/` | ~15 min |

## How the recipes work

Every recipe follows the same shape:

1. **Goal** — the access-control problem in one sentence.
2. **Ingredients** — the policy kinds and server configuration involved.
3. **The policies** — complete YAML you can copy into a `policies.d/`
   directory. Blocks are full policy documents unless marked as fragments.
4. **Run and verify** — commands plus the observable difference that proves
   enforcement (disjoint rows, masked columns, a 202, a rejected join).
5. **Pitfalls** — the mistakes we see most often with that policy kind.

All recipes assume a built `sluice` binary (see the
[quickstart](../getting-started/quickstart.md)) and rely on Sluice's
default-deny posture: an empty policy directory denies everything, so each
recipe starts from an explicit `SqlAccessPolicy` allow.

## Suggested order

Work through the recipes in this order — each reuses ideas from the previous
one, and the last two build on everything else:

1. [Multi-tenant isolation](multi-tenant.md) — selectors, bindings, row filters.
2. [PII masking](pii-masking.md) — column masks and the mask provider catalog.
3. [Break-glass access](break-glass.md) — selector exclusions and the audit trail.
4. [Cross-source joins](cross-source-joins.md) — multiple catalogs under one policy set.
5. [Approval workflow](approval-workflow.md) — human sign-off on sensitive reads.
6. [MCP agents](mcp-agents.md) — the full guardrail stack applied to an AI agent.
