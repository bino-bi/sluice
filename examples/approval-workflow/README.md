<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# approval-workflow

An `ApprovalPolicy` holds a query that names `ssn` or a `salary*` column until a person accepts it.

## Run

```bash
examples/approval-workflow/serve.sh
```

The script creates `data/shop.db` from `seed.sql`. It starts `webhook.py` on port 8090 and Sluice on `http://localhost:8080`. Then run the steps in order from this folder.

| Step | Shows |
|---|---|
| `steps/01-query-held.sh` | The query is held: `ERR_APPROVAL_PENDING`. |
| `steps/02-webhook.sh` | The webhook with the accept and reject links. |
| `steps/03-accept.sh` | The approver opens the accept link. |
| `steps/04-query-runs.sh` | The same query returns rows. |
| `steps/05-query-held-again.sh` | The grant is used up. The query is held again. |
| `steps/06-reject.sh` | The approver opens the reject link. |
| `steps/07-status.sh` | The caller reads the state: `rejected`. |

`webhook.py` stands in for the approver's inbox. It stores each webhook and the newest links under `data/`.

`expected/` holds the real output of each step. `tests/` holds a suite for `sluice policy test policies.d --tests tests`. See [the examples guide](../README.md) for the layout.

Limit: the policy only sees column names written in the query. `SELECT *` returns `ssn` without approval.
