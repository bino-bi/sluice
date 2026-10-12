<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# rest-api

Requests to `POST /v1/query` and their real responses: JSON, an error and CSV.

## Run

```bash
examples/rest-api/serve.sh
```

The script creates `data/shop.db` from `seed.sql` and starts Sluice on `http://localhost:8080`. Then run the steps from this folder, for example `bash steps/01-query.sh`.

| Step | Shows |
|---|---|
| `steps/01-query.sh` | A query with `max_rows`. The result is truncated and carries a warning. |
| `steps/02-denied.sh` | A deny policy refuses the query. The error names the policy. |
| `steps/03-csv.sh` | The same kind of query with `format: csv`. |

`expected/` holds the real output of each step. See [the examples guide](../README.md) for the layout.
