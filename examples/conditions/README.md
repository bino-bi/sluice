<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# conditions

Policies with CEL conditions. This example needs no server and no database.

## Run

```bash
cd examples/conditions
sluice policy test policies.d --tests tests
bash steps/01-explain-error.sh
```

| File | Content |
|---|---|
| `policies.d/allow-orders-small.yaml` | A condition on the query. |
| `policies.d/allow-reports-gold.yaml` | A condition on a claim, with a guard. |
| `policies.d/mask-card-number.yaml` | A condition without a guard. It fails for a subject without the claim. |
| `tests/conditions.yaml` | One suite for all three. |
| `steps/01-explain-error.sh` | Shows the reason for the deny. |

See [the examples guide](../README.md) for the layout.
