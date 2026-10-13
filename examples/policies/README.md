<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# policies

Three policies and two test suites. This example needs no server and no database.

## Run

```bash
cd examples/policies
sluice policy test policies.d --tests tests
```

| File | Content |
|---|---|
| `policies.d/allow.yaml` | An allow policy, a row filter and a column mask. |
| `tests/basic.yaml` | Allow, default deny and a masked column. |
| `tests/select-star.yaml` | `SELECT *` on a masked table. |
| `expected/01-policy-test.txt` | The real output of the test run. |

See [the examples guide](../README.md) for the layout.
