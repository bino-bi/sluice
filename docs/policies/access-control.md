<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Access control

`SqlAccessPolicy` is the gate every query passes first. Its `spec.effect` is either `allow` or
`deny` (lowercase), and two rules govern the outcome:

1. **Default-deny.** If no `SqlAccessPolicy` with `effect: allow` matches the request, it is
   denied — even with an empty policy directory, even if no explicit deny matched. There is no
   way to configure this away.
2. **Deny-override.** If any matching policy has `effect: deny`, the request is denied regardless
   of allows. The highest-priority deny supplies the client-visible `message` and `errorCode`
   (defaults: `access denied` / `ACL_DENIED`).

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata:
  name: deny-salaries
  priority: 900
spec:
  effect: deny
  match:
    any:
      - resources: { tables: ["salaries"] }   # no subjects block = everyone
  message: "salaries is off limits through the gateway"
  errorCode: ACL_DENIED
```

An explicit deny short-circuits: row filters, masks, rewrites, and approvals are not evaluated
for a denied request.

!!! warning "An allow spans the whole statement"
    A clause matches when the subject matches and **at least one** referenced table satisfies its
    `resources` block. An allow on `orders` therefore also admits a query that *joins* `orders`
    with tables the policy never names. Protect sensitive tables with explicit `effect: deny`
    policies — a deny matching any referenced table blocks the whole query, joins included.

## Scoping by action

`resources.actions` restricts a clause to specific statement verbs: `SELECT`, `INSERT`,
`UPDATE`, `DELETE` (always uppercase). A clause that constrains actions never matches a statement
whose verb could not be determined — fail-closed.

!!! warning "The executor is read-only regardless"
    Action scoping is policy-level bookkeeping on top of a hard floor: the execution layer only
    accepts `SELECT`, `EXPLAIN`, `SET`, `SHOW`, and `PRAGMA`. `INSERT`/`UPDATE`/`DELETE` are
    rejected with `ACL_REJECTED` ("write operations are not permitted") even if a policy would
    allow them, and DDL/`COPY`/`ATTACH` fail with `ERR_UNSUPPORTED_SYNTAX`. Listing write actions
    in an allow today changes nothing at runtime.

## Conditions and enforcement mode

Like every enforcement kind, `SqlAccessPolicy` supports [`conditions`](conditions.md) (all must be
true for the policy to apply; a runtime error denies the request) and
[`enforcementMode`](index.md#shared-spec-fields) (`Audit`/`DryRun` record a shadow match without
affecting the decision — useful to stage a new deny and watch who *would* be blocked).

## Where access control sits

The precedence across kinds is structural, not configurable:

**deny > reject > approval.** A deny (explicit or default) ends evaluation. Otherwise, fired
[reject rules](guardrails.md) flip an allow to a rejection. Only a clean allow can be parked for
[human approval](approvals.md). Row filters, masks, and rewrites then shape the allowed query.

## Recipes

**Allow analysts on one schema:**

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata:
  name: allow-analysts-reporting
  priority: 100
spec:
  effect: allow
  match:
    any:
      - subjects: { groups: ["analysts"] }
        resources:
          catalogs: ["shop"]
          schemas: ["reporting"]
```

**Hard-deny a table for everyone** — a clause with only a `resources` block matches every
subject; the high priority makes this deny supply the error message even if other denies overlap:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata:
  name: deny-raw-events
  priority: 1000
spec:
  effect: deny
  match:
    any:
      - resources:
          catalogs: ["lake"]
          tables: ["raw_events"]
  message: "query the curated events view instead"
```

**Allow only SELECT for one API key** — the key id comes from the
[SubjectBinding](subjects.md) `apiKeys[].id`:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata:
  name: allow-export-bot-select
  priority: 100
spec:
  effect: allow
  match:
    any:
      - subjects: { apiKeys: ["export-bot"] }
        resources:
          catalogs: ["shop"]
          actions: ["SELECT"]
```
