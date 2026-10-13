<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# subjects

Three `SubjectBinding` objects: an API key, a JWT issuer, and an API key with a rate limit and a daily budget.

## Run

```bash
examples/subjects/serve.sh
```

The script creates `data/shop.db` from `seed.sql` and starts Sluice on `http://localhost:8080`. Then run the steps in order from this folder.

| Step | Shows |
|---|---|
| `steps/01-hash.sh` | `sluice apikey hash` prints the hash for `hashRef`. |
| `steps/02-apikey.sh` | A query with `X-Api-Key`. |
| `steps/03-jwt.sh` | A query with a JWT. `mint-jwt.py` signs `token-claims.json`. |
| `steps/04-agent.sh` | The agent reads 3 rows. This uses up its row budget. |
| `steps/05-agent-over-budget.sh` | The next query gets `ERR_BUDGET_EXCEEDED`. |
| `steps/06-agent-too-fast.sh` | The third query in a row gets `ERR_RATE_LIMITED`. |
| `steps/07-budget-show.sh` | `sluice budget show` prints the usage of the agent. |

`mint-jwt.py` stands in for an identity provider. It makes an HS256 token with the demo secret from `serve.sh`.

`expected/` holds the real output of each step. See [the examples guide](../README.md) for the layout.
