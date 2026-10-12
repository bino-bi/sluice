<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# data-sources

One `DataSource` file for each type that needs a service: `postgres`, `mysql`, `s3`, `http` and `motherduck`.

The steps check the files. They do not connect to a service, so this example has no `serve.sh`. Run the steps from this folder, for example `bash steps/01-validate.sh`.

| Step | Shows |
|---|---|
| `steps/01-validate.sh` | Every file in `policies.d` is valid. |
| `steps/02-check.sh` | `sluice datasource check` lists each data source and its type. |
| `steps/03-check-invalid.sh` | An invalid file in `invalid/` ends the check with status 1. |

`expected/` holds the real output of each step. See [the examples guide](../README.md) for the layout. For `sqlite` and `duckdb_file` data sources that serve queries, see [cross-source-join](../cross-source-join/README.md).
