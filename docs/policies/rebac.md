<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# ReBAC (OpenFGA)

A `RelationshipPolicy` asks an OpenFGA store if a subject has a relation to a table. Without the relation, Sluice denies the query.

## Example

Goal: only a `viewer` of a table reads it.

The policy runs only as the `rebac` member of a composite engine. See [Policy engines](../architecture/policy-engines.md).

```yaml
--8<-- "examples/rebac/server.yaml:policies"
```

```yaml
--8<-- "examples/rebac/policies.d/relationship.yaml:2"
```

The example uses a stub in place of OpenFGA. Point `backend.endpoint` at your OpenFGA server. The stub knows one tuple. A real model needs the type `table` with the relation `viewer`.

```json
--8<-- "examples/rebac/tuples.json"
```

Alice reads the table.

```bash
--8<-- "examples/rebac/steps/01-alice.sh"
```

```json
--8<-- "examples/rebac/expected/01-alice.txt"
```

Bob sends the same query.

```json
--8<-- "examples/rebac/expected/02-bob.txt"
```

For Bob, Sluice sent this body to `POST <endpoint>/stores/<storeId>/check`.

```json
--8<-- "examples/rebac/expected/03-check-request.txt"
```

**What happened:** Sluice ran each check for each table that the policy matches. OpenFGA answered `allowed: false` for Bob. A passed check allows nothing: Alice also needs the `SqlAccessPolicy` of the `yaml` member.

## Fields

| Field | Default | Meaning |
|---|---|---|
| `backend.endpoint` | | URL of the OpenFGA server. Required. |
| `backend.storeId` | | Store id. Required. |
| `backend.authorizationModelId` | | Model id for the check. |
| `backend.tokenRef` | | Secret, sent as a Bearer token. |
| `backend.timeout` | 3s | Timeout of one check. |
| `checks[].objectTemplate` | | The `object`. Required. |
| `checks[].relation` | | The `relation`. Required. |
| `checks[].subjectTemplate` | `user:{{subject.id}}` | The `user`. |

Templates know `{{catalog}}`, `{{schema}}`, `{{table}}`, `{{subject.id}}` and `{{subject.email}}`. `exclude` takes single tables out of the check.

Sluice caches each answer for `policies.rebac.cacheTtl` (default 10s). A reload clears the cache.

## Limits

- A check decides on a whole table. It does not filter rows.
- If OpenFGA fails or times out, the query gets 500 `ERR_INTERNAL`.
- Write placeholders without spaces. Sluice sends `{{ table }}` and unknown placeholders as plain text.
- With `enforcementMode: Audit` or `DryRun`, a failed check does not deny. `GET /admin/subjects/explain` lists the policy under `shadow` for passed and failed checks alike.
- The selector cannot use `resources.tags`.

## See also

- [Policy engines](../architecture/policy-engines.md)
- [Access control](access-control.md)
- [Row filters](row-filters.md)
