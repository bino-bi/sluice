<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# The policy model

Everything Sluice enforces is declared in YAML objects loaded from a policy directory. There is no
imperative configuration: you describe *who* may touch *what* and *how results are shaped*, and the
gateway compiles those objects into an immutable snapshot that every query is evaluated against.

!!! warning "Use the right apiVersion"
    Every policy object declares `apiVersion: sluice.bino.bi/v1alpha1`, except the two v1beta1
    kinds `DataClassification` and `RelationshipPolicy`, which declare
    `apiVersion: sluice.bino.bi/v1beta1`. Any other value is rejected at load time.

## The 11 kinds

| Kind | apiVersion | Purpose |
| ---- | ---------- | ------- |
| [`SqlAccessPolicy`](access-control.md) | v1alpha1 | Allow or deny access; without a matching allow, the request is denied |
| [`RowFilterPolicy`](row-filters.md) | v1alpha1 | Inject a WHERE predicate so subjects only see their rows |
| [`ColumnMaskPolicy`](column-masks.md) | v1alpha1 | Replace column values (null, partial, hash, FPE, fake, …) |
| [`QueryRejectPolicy`](guardrails.md) | v1alpha1 | Reject whole queries that match a CEL rule |
| [`QueryRewritePolicy`](guardrails.md) | v1alpha1 | Cap LIMIT, sample results, or bound the query timeout |
| [`ApprovalPolicy`](approvals.md) | v1alpha1 | Park sensitive queries until a human approves them |
| [`DataSource`](../operations/data-sources.md) | v1alpha1 | Attach a database or object store as a catalog |
| [`SubjectBinding`](subjects.md) | v1alpha1 | Map JWT issuers and API keys onto subjects, rate limits, budgets |
| [`AuditSink`](../security/audit.md) | v1alpha1 | Where the hash-chained audit trail is written |
| [`DataClassification`](classification.md) | v1beta1 | Tag resources so policies can match on tags instead of names |
| [`RelationshipPolicy`](rebac.md) | v1beta1 | Delegate table-level checks to OpenFGA (ReBAC) |

## Anatomy of a policy object

Every object has the same four top-level fields — `apiVersion`, `kind`, `metadata`, `spec`:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata:
  name: allow-analysts-orders   # RFC-1123 label, max 63 chars
  priority: 100                 # 0-1000, higher wins conflicts
  labels:
    team: data-platform         # optional, informational
spec:
  effect: allow
  match:
    any:
      - subjects:
          groups: ["analysts"]
        resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["orders"]
```

- `metadata.name` must be an RFC-1123 label: lowercase letters, digits, and dashes, starting and
  ending with a letter or digit, at most 63 characters. The kind/name pair must be unique across
  the whole directory — a duplicate is a load error.
- `metadata.priority` is an integer from 0 to 1000. Policies evaluate priority descending, then
  name ascending, and priority decides [conflicts](matching.md#priority-and-specificity).

## Shared spec fields

Every enforcement kind supports the same four framing fields alongside its kind-specific body:

| Field | Meaning |
| ----- | ------- |
| `match` | Selector deciding when the policy applies — see [Matching](matching.md) |
| `exclude` | Selector carving subjects or tables back out of `match` |
| `conditions` | Named CEL expressions that gate the policy — see [Conditions](conditions.md) |
| `enforcementMode` | `Enforce` (default), `Audit`, or `DryRun` |

`Audit` and `DryRun` policies match and are recorded as shadow outcomes (visible in
`sluice policy explain --json` as `shadow` entries) but never change the decision. The two modes currently behave identically
at evaluation time — the two names exist so you can label a permanent observer differently from a
staged rollout. The same mask policy in each mode:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata: { name: mask-email-enforce }
spec:
  enforcementMode: Enforce      # actually masks the column
  match:
    any:
      - resources: { tables: ["customers"], columns: ["email"] }
  mask: { type: partial, args: { showFirst: 1, showLast: 0 } }
---
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata: { name: mask-email-audit }
spec:
  enforcementMode: Audit        # records a shadow match, column stays clear
  match:
    any:
      - resources: { tables: ["customers"], columns: ["email"] }
  mask: { type: partial, args: { showFirst: 1, showLast: 0 } }
---
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata: { name: mask-email-dry-run }
spec:
  enforcementMode: DryRun       # same behaviour as Audit; use while staging
  match:
    any:
      - resources: { tables: ["customers"], columns: ["email"] }
  mask: { type: partial, args: { showFirst: 1, showLast: 0 } }
```

## Default-deny and the minimum viable set

An empty policy directory is valid configuration — it means **deny everything**. For any query to
succeed you need at least three objects: a `DataSource` (something to query), a `SubjectBinding`
(someone to be), and a `SqlAccessPolicy` with `effect: allow` (permission to ask):

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: DataSource
metadata: { name: shop }
spec:
  type: sqlite
  path: ./data/shop.db
  readonly: true
---
apiVersion: sluice.bino.bi/v1alpha1
kind: SubjectBinding
metadata: { name: reporting-keys }
spec:
  apiKeys:
    - id: reporting-bot
      hashRef: secret://env/REPORTING_BOT_KEY_HASH
      groups: ["analysts"]
---
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata: { name: allow-analysts, priority: 100 }
spec:
  effect: allow
  match:
    any:
      - subjects: { groups: ["analysts"] }
        resources: { catalogs: ["shop"] }
```

## Loading policies

The server loads every `*.yaml` / `*.yml` file under `policies.directory` (default
`./policies.d`), recursively. Dot-directories and directories named `testdata` or `tests` are
skipped — which is why [test suites](testing.md) can live in `policies.d/tests/`. A single file
may contain multiple objects separated by `---`.

Validate before you deploy:

```console
$ sluice policy validate ./policies.d --strict
policies OK: ./policies.d (3 objects, digest 8b509e21d8c7)
```

`--strict` additionally rejects unknown YAML fields — recommended in CI, where a typo like
`prioritty:` should fail the build instead of being silently ignored. The full field-by-field
schema is generated in the [policy schema reference](../reference/policy-schema.md).
