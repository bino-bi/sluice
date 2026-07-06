<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# MCP agents

**Goal:** give Claude (or any MCP client) governed SQL access. The agent gets
a narrow, rate-limited, budgeted, always-audited window onto your data — the
same parse → policy → rewrite → audit pipeline as every other caller. Runnable
version: `examples/mcp-agent/`.

## Claude Desktop configuration

Claude Desktop spawns Sluice as a child process and speaks MCP over stdio. The
stdio transport authenticates **once at startup** from a static credential and
pins that identity onto every tool call:

```json
{
  "mcpServers": {
    "sluice": {
      "command": "/ABSOLUTE/PATH/TO/sluice/bin/sluice",
      "args": [
        "mcp",
        "--config=/ABSOLUTE/PATH/TO/sluice/examples/mcp-agent/server.yaml",
        "--policies-dir=/ABSOLUTE/PATH/TO/sluice/examples/mcp-agent/policies.d",
        "--api-key=mcp.supersecret"
      ],
      "env": {
        "SLUICE_APIKEY_PEPPER": "mcp-agent-demo-pepper",
        "SLUICE_AUDIT_GENESIS": "mcp-agent-demo-genesis",
        "SLUICE_APIKEY_MCP_HASH": "<output of: sluice apikey hash --pepper mcp-agent-demo-pepper --id mcp --material supersecret>"
      }
    }
  }
}
```

Paths must be absolute — Claude Desktop does not resolve relative paths. A JWT
works too (`--jwt` or `SLUICE_MCP_TOKEN`); `--allow-anonymous` runs without a
credential, in which case default-deny blocks every query unless a policy
grants the anonymous subject.

## The agent-safety bundle

Three layers, all hot-reloadable — narrow access, bounded result sizes, and
per-day spend caps:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata:
  name: allow-agents
  priority: 100
spec:
  effect: allow
  match:
    any:
      - subjects:
          groups: ["agents"]
        resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["products"]
---
apiVersion: sluice.bino.bi/v1alpha1
kind: QueryRewritePolicy
metadata:
  name: agent-guardrails
  priority: 100
spec:
  match:
    any:
      - subjects:
          groups: ["agents"]
  rewrite:
    limit:
      max: 500        # inject LIMIT 500, clamp anything larger
    timeout: 10s
---
apiVersion: sluice.bino.bi/v1alpha1
kind: SubjectBinding
metadata:
  name: mcp-local
spec:
  claims:
    subjectId: "mcp-agent"
  rateLimit:
    rps: 2
    burst: 5
  budget:
    rowsPerDay: 100000
    cpuSecondsPerDay: 600
  apiKeys:
    - id: "mcp"
      hashRef: "secret://env/SLUICE_APIKEY_MCP_HASH"
      groups: ["agents"]
```

If the agent's tables contain PII, layer the same `ColumnMaskPolicy` objects
as in the [PII-masking recipe](pii-masking.md) — masks apply to MCP callers
exactly as to REST callers.

## An agent session

The agent sees **nine tools**: `execute_sql`, `list_catalogs`, `list_tables`,
`describe_table`, `whoami`, `explain_access`, `list_accessible_tables`,
`check_approval`, `await_approval`. A well-behaved session starts with
discovery instead of trial-and-error SQL:

1. `whoami {}` — who am I? Subject `mcp-agent`, groups `["agents"]`.
2. `list_accessible_tables {}` — which tables can this identity actually
   reach? Here: `shop.main.products`, nothing else.
3. `explain_access { table: "shop.main.products" }` — which policies apply and
   what will happen to a query (filters, masks, and the matched policies)
   before running it.
4. `execute_sql { sql: "SELECT name, price_cents FROM shop.main.products ORDER BY price_cents DESC", row_limit: 100 }`
   — rows come back with `LIMIT` injected per the rewrite policy.

Verify the guardrail: ask the agent for something outside the policy. The
`execute_sql` call returns an `ACL_DENIED` error *as tool output*, so the
model reads it, can call `explain_access` with the failing SQL to see why, and
self-corrects without a protocol crash. Every call — allowed or denied — lands
in the audit log under the pinned subject.

!!! warning "Not yet implemented"
    Prompt-side niceties like schema descriptions from your own metadata store
    are not part of the tool surface; `describe_table` returns column names,
    types, and nullability only. There is also no OpenTelemetry tracing of
    tool calls yet —
    the audit log is the source of truth for agent activity.

## Pitfall: an empty table list is a policy gap, not a bug

Default-deny means `list_accessible_tables` returns exactly what your allow
policies grant. If it comes back empty, the agent has no access — Sluice is
working as designed and the fix is a `SqlAccessPolicy`, not a server setting.
The inverse holds too: every table in that list is one prompt injection away
from being queried, so treat the allow-list as the agent's blast radius and
start narrow.

## See also

- [MCP reference](../reference/mcp.md) — all nine tools with argument schemas.
- [Approval workflow](approval-workflow.md) — `check_approval` / `await_approval` in action.
- [Subjects, keys & budgets](../policies/subjects.md) — rate limits and daily budgets.
