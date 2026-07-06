<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Relationship-based access (ReBAC)

`RelationshipPolicy` (apiVersion `sluice.bino.bi/v1beta1`) delegates table-level access decisions
to an [OpenFGA](https://openfga.dev) store. Reach for it when access depends on per-object
relationships that do not reduce to groups — "Alice may read the tables of projects she is a
member of" — and that relationship graph already lives (or belongs) in a ReBAC system rather
than in YAML selectors.

!!! warning "Table gate, not row filter"
    A relationship check gates access to a **table per query** — it answers "may this subject
    touch this table at all?". It does not filter rows inside the table. For per-row isolation
    combine it with a [RowFilterPolicy](row-filters.md).

## Enabling the engine

RelationshipPolicies are evaluated by the `rebac` composite member. Without it, the objects load
but never run:

```yaml
# fragment of sluice.yaml — server configuration, not a policy
policies:
  directory: ./policies.d
  engine: composite
  composite:
    members: [yaml, rebac]
  rebac:
    cacheTtl: 10s      # result cache TTL (default 10s)
    cacheSize: 10000   # LRU entries (default 10000)
```

The composite merges member decisions deny-overrides: with `[yaml, rebac]` a query still needs
its [SqlAccessPolicy allow](access-control.md), *and* every matching relationship check must
pass. When no RelationshipPolicy matches a query, the rebac member abstains and the YAML
decision stands alone.

## The kind

```yaml
apiVersion: sluice.bino.bi/v1beta1
kind: RelationshipPolicy
metadata: { name: project-table-viewers }
spec:
  match:
    any:
      - resources: { catalogs: ["shop"] }
  backend:
    type: openfga
    endpoint: http://openfga.internal:8080
    storeId: 01HXYZSTORE0000000000000000
    authorizationModelId: 01HXYZMODEL0000000000000000   # optional: pin a model version
    tokenRef: secret://env/OPENFGA_TOKEN                # optional: sent as Bearer
    timeout: 3s                                         # HTTP client timeout (default 3s)
  checks:
    - objectTemplate: "table:{{catalog}}.{{schema}}.{{table}}"
      relation: viewer
      subjectTemplate: "user:{{subject.id}}"            # this is the default
```

`backend.endpoint` and `backend.storeId` are required, as is at least one check with an
`objectTemplate` and `relation`. The only supported backend type is `openfga`. Templates accept
exactly five placeholders: `{{catalog}}`, `{{schema}}`, `{{table}}`, `{{subject.id}}`,
`{{subject.email}}`. `subjectTemplate` defaults to `user:{{subject.id}}`.

## Evaluation semantics

For every table the query references that the selector matches, Sluice runs **every** check by
rendering the templates and calling `POST <endpoint>/stores/<storeId>/check` with
`tuple_key: {user, relation, object}`:

| Outcome | Result |
| ------- | ------ |
| All checks answer `allowed: true` | Allow (merged with the other engines) |
| Any check answers `allowed: false` | Deny — `ACL_DENIED`, "relationship check failed on \<table\>" |
| Backend error / non-2xx / timeout | Query fails, fail-closed — never a silent allow |
| No RelationshipPolicy matched | Abstain — other composite members decide |

`enforcementMode: Audit` or `DryRun` records shadow matches on the decision instead of
denying — surfaced via `GET /admin/subjects/explain`, useful to stage a new relationship
model. `exclude` carves tables back out per table.

Check results (positive *and* negative) are cached in an LRU keyed on
`object#relation@subject`. The cache TTL is controlled only by `policies.rebac.cacheTtl` in
the server configuration (the per-policy `spec.backend.cacheTtl` field is parsed but currently
ignored); errors are never cached. The cache is purged on every
[policy reload](../operations/hot-reload.md). Budget one OpenFGA round-trip per uncached
(table × check) pair per query.

## Recipes

**Viewer relation on every table of a catalog** — the model side needs a matching type
definition (`type table` with a `viewer` relation) and tuples like
`user:alice viewer table:shop.main.orders`:

```yaml
apiVersion: sluice.bino.bi/v1beta1
kind: RelationshipPolicy
metadata: { name: shop-table-viewers }
spec:
  match:
    any:
      - resources: { catalogs: ["shop"] }
  backend:
    type: openfga
    endpoint: http://openfga.internal:8080
    storeId: 01HXYZSTORE0000000000000000
    tokenRef: secret://env/OPENFGA_TOKEN
  checks:
    - objectTemplate: "table:{{catalog}}.{{schema}}.{{table}}"
      relation: viewer
```

**Stage a stricter relation in shadow mode** — shadow matches are recorded on the decision and
surfaced via `GET /admin/subjects/explain`; inspect them for would-be denials before flipping
to `Enforce`:

```yaml
apiVersion: sluice.bino.bi/v1beta1
kind: RelationshipPolicy
metadata: { name: shop-table-editors-staged }
spec:
  enforcementMode: Audit
  match:
    any:
      - resources: { catalogs: ["shop"], schemas: ["finance"] }
  backend:
    type: openfga
    endpoint: http://openfga.internal:8080
    storeId: 01HXYZSTORE0000000000000000
  checks:
    - objectTemplate: "table:{{catalog}}.{{schema}}.{{table}}"
      relation: editor
```
