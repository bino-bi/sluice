<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# ReBAC (OpenFGA)

A `RelationshipPolicy` asks [OpenFGA](../reference/glossary.md), a server that stores relations, if a subject has a relation to a table. Without the relation, Sluice denies the query. This is [ReBAC](../reference/glossary.md): relationship-based access control.

## Example

Goal: only a `viewer` of a table reads it.

The policy runs only as the `rebac` member of a composite engine. See [Policy engines](../architecture/policy-engines.md).

```yaml
--8<-- "examples/rebac/server.yaml:policies"
```

```yaml
--8<-- "examples/rebac/policies.d/relationship.yaml:2"
```

The example uses a stub in place of OpenFGA. The stub knows one tuple. A real model needs the type `table` with the relation `viewer`.

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

```bash
--8<-- "examples/rebac/steps/02-bob.sh"
```

```json
--8<-- "examples/rebac/expected/02-bob.txt"
```

For Bob, Sluice sent this body to `POST <endpoint>/stores/<storeId>/check`.

```bash
--8<-- "examples/rebac/steps/03-check-request.sh"
```

```json
--8<-- "examples/rebac/expected/03-check-request.txt"
```

**What happened:** Sluice ran each check for each table that the policy matches. OpenFGA answered `allowed: false` for Bob. A passed check allows nothing: Alice also needs the `SqlAccessPolicy` of the `yaml` member.

## Fields

| Field | Type | Default | Meaning |
|---|---|---|---|
| `backend.endpoint` | string | required | URL of the OpenFGA server. |
| `backend.storeId` | string | required | Store id. |
| `backend.authorizationModelId` | string | | Model id for the check. |
| `backend.tokenRef` | string | | Secret, sent as a Bearer token. |
| `backend.timeout` | duration | 3s | Timeout of one check. |
| `checks[].objectTemplate` | string | required | The `object`. |
| `checks[].relation` | string | required | The `relation`. |
| `checks[].subjectTemplate` | string | `user:{{subject.id}}` | The `user`. |

Templates know `{{catalog}}`, `{{schema}}`, `{{table}}`, `{{subject.id}}` and `{{subject.email}}`. `exclude` takes single tables out of the check.

Sluice caches each answer for `policies.rebac.cacheTtl` (default 10s). A reload clears the cache.

## Limits

- A check decides on a whole table. It does not filter rows.
- If OpenFGA fails, the query gets 500 `ERR_INTERNAL`. If it does not answer within `backend.timeout`, the query gets 504 `ERR_TIMEOUT`.
- Write placeholders without spaces. Sluice sends `{{ table }}` and unknown placeholders as plain text.
- With `enforcementMode: Audit` or `DryRun`, a failed check does not deny. `GET /admin/subjects/explain` lists the policy under `shadow` for passed and failed checks alike.
- The selector cannot use `resources.tags`.

## See also

- [Policy engines](../architecture/policy-engines.md)
- [Access control](access-control.md)
- [Row filters](row-filters.md)
