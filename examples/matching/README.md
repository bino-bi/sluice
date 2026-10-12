<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# matching

Policies and suites that show how `match` and `exclude` work. This example needs no server and no database.

## Run

```bash
cd examples/matching
sluice policy test policies.d --tests tests
```

| Suite | Shows |
|---|---|
| `tests/any-all.yaml` | `any` and `all`. |
| `tests/subjects.yaml` | Groups, roles and every `jwtClaims` operator. |
| `tests/resources.yaml` | Catalogs, schemas, tables and wildcards. |
| `tests/exclude.yaml` | `exclude` for each policy kind. |
| `tests/priority.yaml` | Which column mask wins. |

See [the examples guide](../README.md) for the layout.
