<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Policies

Sluice policies are YAML Kubernetes-style objects. The five Kinds that
shape every request are:

| Kind                 | Purpose                                                 |
| -------------------- | ------------------------------------------------------- |
| `SqlAccessPolicy`    | Allow or deny a subject on a set of tables/statements.  |
| `RowFilterPolicy`    | Inject a WHERE predicate for matching subjects+tables.  |
| `ColumnMaskPolicy`   | Substitute a masked expression at every column ref.     |
| `QueryRejectPolicy`  | Reject statements matching a static rule or CEL expr.   |
| `SubjectBinding`     | Map an issuer + claim path to a canonical subject.      |

A `DataSource` Kind attaches a catalog; an `AuditSink` Kind configures
persistence. See the [policy schema reference](../reference/policy-schema.md)
for the complete surface.

## Default-deny

An empty policy directory rejects every query. There is no implicit
`allow-all` rule; the composition root publishes an empty snapshot at
boot, and the policy engine resolves ambiguity in favour of deny.

## Conflict resolution

Multiple matching policies resolve deterministically:

1. **Deny-override** — any matching `deny` in an `SqlAccessPolicy`
   wins.
2. **Row filters** — combined `AND` for `restrictive` policies, `OR`
   for `permissive`. The tightest intersection of matching policies is
   applied.
3. **Column masks** — priority desc → specificity desc → lexicographic
   name asc. Higher priority masks shadow lower ones on the same
   column.

## Reloading

Policies reload without a restart through three mechanisms:

- `fsnotify` watcher on the policy directory (debounced 250 ms).
- `SIGHUP` signal.
- `POST /admin/reload` on the admin port.

All three end in `config.Registry.Publish`, which atomically swaps the
compiled policy snapshot and invalidates the schema cache.
