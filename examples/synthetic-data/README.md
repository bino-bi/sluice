<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# synthetic-data

A synthetic data source that serves generated rows in place of the real customers and orders. The orders keep their link to the customers.

## Run

```bash
examples/synthetic-data/serve.sh
```

The script creates `data/shop.db` from `seed.sql`. It builds the snapshots of `shopsynth` and `shopnoisy` with `sluice synth build` into `data/synthetic`. Then it starts Sluice on `http://localhost:8080` and the admin API on `http://localhost:9091`. Run the steps in order from this folder.

| Step | Shows |
|---|---|
| `steps/01-query-synthetic.sh` | Orders per product in the synthetic data. The rare product is gone. |
| `steps/02-query-source-denied.sh` | The same query on the real data is denied. |
| `steps/03-datasources.sh` | The state of each data source, with `status: ready` for `shopsynth`. |
| `steps/04-report.sh` | The build report: generator and distance per column. |
| `steps/05-join.sh` | Orders per city through a join. Every synthetic order finds its customer. |
| `steps/06-correlation.sh` | The correlation of `amount` and `quantity` in the synthetic orders. |
| `steps/07-query-noisy.sh` | Orders per product in `shopnoisy`, built with noise on the counts. |
| `steps/08-noisy-range.sh` | The range of `amount` in `shopnoisy`, from the public bounds. |
| `steps/09-report-noisy.sh` | The build report of `shopnoisy` with the noise budget. |

`expected/` holds the real output of each step. See [the examples guide](../README.md) for the layout.
