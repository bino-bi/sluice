<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# cli

Commands of the `sluice` binary that need no server. They read the policy directory `policies.d`.

## Run

Run the steps from this folder, for example `bash steps/01-policy-validate.sh`.

| Step | Shows |
|---|---|
| `steps/01-policy-validate.sh` | The policy directory is valid. |
| `steps/02-policy-explain.sh` | The policies that apply to one subject and one table. |
| `steps/03-validate-broken.sh` | A policy in `broken.d` does not compile. The command ends with exit status 3. |

`expected/` holds the real output of each step. See [the examples guide](../README.md) for the layout.
