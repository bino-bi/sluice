<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# conditions

Policies with CEL conditions. This example needs no server and no database.

## Run

```bash
cd examples/conditions
sluice policy test policies.d --tests tests
bash steps/01-test-claim.sh
bash steps/02-explain-error.sh
```

| File | Content |
|---|---|
| `policies.d/allow-reports-gold.yaml` | A condition on a claim, with a guard. |
| `policies.d/allow-orders-small.yaml` | A condition on the query. |
| `policies.d/mask-card-number.yaml` | A condition without a guard. It fails for a subject without the claim. |
| `policies.d/query-values.yaml` | One reject rule per `query` value, for the suite below. |
| `tests/claim.yaml` | The suite for the claim condition. |
| `tests/conditions.yaml` | The suite for the other two conditions. |
| `tests/query-values.yaml` | What the `query` values see in a join, a `UNION` and a subquery. |
| `steps/01-test-claim.sh` | Runs the claim suite. |
| `steps/02-explain-error.sh` | Shows the reason for the deny. |

See [the examples guide](../README.md) for the layout.
