<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# policies-basics

One allow policy, one deny policy in `Audit` mode and one file with an error. This example needs no server and no database.

## Run

```bash
cd examples/policies-basics
bash steps/01-validate.sh
```

| Step | Shows |
|---|---|
| `steps/01-validate.sh` | `sluice policy validate` on a valid policy directory. |
| `steps/02-validate-invalid.sh` | The error for the file in `invalid/`. The exit status is 3. |
| `steps/03-explain.sh` | The deny in `Audit` mode does not deny. It shows under `shadow`. |

`tests/` holds a suite for `sluice policy test policies.d --tests tests`. See [the examples guide](../README.md) for the layout.
