<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# policy-testing

Suites for `sluice policy test`: one that uses every `expect` field and one that fails on purpose. This example needs no server and no database.

## Run

```bash
cd examples/policy-testing
sluice policy test policies.d --tests tests
bash steps/01-failing-case.sh
```

| File | Content |
|---|---|
| `tests/expect.yaml` | Cases for every `expect` field. |
| `tests/schema.yaml` | When a table needs a `schema` entry. |
| `failing/wrong-outcome.yaml` | A suite with one wrong case. It is not in `tests/`, so the first command passes. |
| `steps/01-failing-case.sh` | Runs the failing suite. The exit status is 3. |
| `steps/02-json.sh` | The same run with `--json`. |

See [the examples guide](../README.md) for the layout.
