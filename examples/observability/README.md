<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# observability

Find the cause of a failed query in the log and read the metrics.

## Run

```bash
examples/observability/serve.sh
```

The script creates `data/shop.db` from `seed.sql` and starts Sluice on `http://localhost:8080`. It writes the log to `data/sluice.log`. Run the steps in order from this folder, for example `bash steps/01-query.sh`.

| Step | Shows |
|---|---|
| `steps/01-query.sh` | A query that works. |
| `steps/02-failed-query.sh` | A query that DuckDB refuses. The client gets `ERR_QUERY_FAILED`. |
| `steps/03-log.sh` | The log line with the cause. |
| `steps/04-metrics.sh` | The audit metrics after two queries. |
| `steps/05-validate-tracing.sh` | The `tracing` block in `tracing.yaml` is valid. |

`expected/` holds the real output of each step. See [the examples guide](../README.md) for the layout.
