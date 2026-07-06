<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Policy engines

Sluice evaluates policies through a pluggable engine selected by `policies.engine` in the server
config:

| Value | Behavior |
| --- | --- |
| `yaml` (default) | The built-in engine over the YAML policy kinds in `policies.d/`. |
| `opa` | Embedded Open Policy Agent — Rego modules decide instead of YAML policies. |
| `composite` | Runs the engines in `policies.composite.members` in order and merges their decisions. |

`policies.composite.members` defaults to `["yaml"]` and may additionally include `"opa"` and
`"rebac"`. The ReBAC member is not a standalone engine value — it only runs inside a composite.

```yaml
# sluice.yaml (server configuration, not a policy document)
policies:
  directory: ./policies.d
  engine: composite
  composite:
    members: ["yaml", "opa", "rebac"]
  opa:
    moduleDir: ./rego
    query: data.sluice.main
```

## The decision contract

Every engine — and the composite of them — produces the same decision shape:

- an **outcome**: allow, deny, or reject (plus an *abstained* flag on a no-opinion deny),
- **row filters** per table, with their combine mode,
- **column masks** per column,
- **rewrite effects** (row cap, sample, timeout),
- **rejections** (rule name, message, code),
- an optional **approval requirement**, and
- the **applied policy names** (with shadow entries for `Audit`/`DryRun` policies).

This is exactly the contract `sluice policy test` asserts against, so a fixture suite written for
the YAML engine describes the behavior any engine must reproduce. See
[Testing policies](../policies/testing.md).

## YAML engine

The default engine compiles `policies.d/` into an immutable snapshot and evaluates it
deterministically:

- Policies are ordered by **priority descending, then name ascending**.
- **Deny overrides**: any matched `effect: deny` wins, and the highest-priority deny supplies the
  message and error code.
- **No allow means deny**: if no `SqlAccessPolicy` with `effect: allow` matched, the request is
  denied by default (recorded as an *abstain* so a composite member can still allow).
- **Row filters fold** per table by the policy's `combine` mode — `restrictive` ANDs predicates,
  `permissive` ORs them.
- **Masks resolve per column** by priority descending, then selector specificity descending, then
  name ascending; the first candidate to claim a column wins.
- **Rewrites fold restrictively**: minimum row cap, minimum timeout, first sample instruction.
- A **condition that errors at runtime denies the whole request** — an error is never treated as
  true or false.

## OPA engine

`opa` embeds Open Policy Agent in-process — no sidecar. Top-level `*.rego` files (non-recursive)
under `policies.opa.moduleDir` are compiled at startup and recompiled on every policy reload; the
decision is read from `policies.opa.query`, default `data.sluice.main`. Rego receives a structured
input document (subject, action, tables, query shape, request facts) and must return a strict
output contract: `allow`, `abstain`, `deny_reason`, `row_filters`, `column_masks`, `rejections`.
Unknown output fields are rejected and the request fails closed. Returned predicates and masks are
recompiled through the YAML engine's compile paths, so Rego authors never render SQL text. Full
input/output reference and examples: [OPA policies](../policies/opa.md).

## ReBAC member

The `rebac` member evaluates `RelationshipPolicy` documents against an OpenFGA backend. For each
table the policy's selector matches, every check runs: the `objectTemplate` and `subjectTemplate`
are rendered (for example `document:{{catalog}}.{{schema}}.{{table}}` and `user:{{subject.id}}`)
and posted to the store's check endpoint. All checks true means allow for that request; any false
means deny; a backend error fails closed as an error; no matching policy abstains. Results are
cached in an LRU (`policies.rebac.cacheTtl`, default 10 s; errors are never cached). Details:
[ReBAC policies](../policies/rebac.md).

## Composite merging

The composite runs its members in order and merges:

1. **Any member error → the request errors** (fail-closed).
2. **First non-abstained deny → final deny** (deny overrides, whichever engine raised it).
3. **Rejections union**: any fired reject rule from any member rejects the query.
4. **At least one member must allow**: if every member abstained, the composite default-denies.
5. **On allow, restrictions merge across all members** — extra restriction is always safe. Row
   filters AND-combine per table (never OR'd across engines), column masks are first-member-wins
   (losing masks are recorded as shadow entries), rewrite effects fold restrictively, and approval
   requirements accumulate.

So a query passes only if some engine affirmatively allows it, no engine denies it, and it then
carries the union of every engine's filters and masks.

## Determinism and introspection

Evaluation is deterministic: the same snapshot, subject, and query always produce the same
decision — there is deliberately no clock variable in policy expressions, which is also what makes
the rewrite cache sound. To see a decision without running a query:

- `sluice policy explain --user U --table cat.sch.tbl` on a policy directory ([CLI](../reference/cli.md)),
- `GET /admin/subjects/explain` against a running server ([Admin API](../reference/admin-api.md)),
- the `explain_access` MCP tool for agents ([MCP](../reference/mcp.md)).
