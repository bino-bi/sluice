<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Data sources

A `DataSource` object attaches one database or file store as a catalog. The files live in the policy directory.

## Example

Goal: attach a SQLite file and a DuckDB file, then join them in one query.

```yaml
--8<-- "examples/cross-source-join/policies.d/datasource-shop.yaml:2"
```

```yaml
--8<-- "examples/cross-source-join/policies.d/datasource-ref.yaml:2"
```

```bash
--8<-- "examples/cross-source-join/steps/01-join.sh"
```

```json
--8<-- "examples/cross-source-join/expected/01-join.txt"
```

What happened: `metadata.name` became the catalog name, so the tables are `shop.main.orders` and `ref.main.countries`. Sluice attached both files at start.

`limits.disableCrossCatalog: true` rejects a query on several catalogs with `ACL_REJECTED`. See [Cross-source joins](../cookbook/cross-source-joins.md).

## Types

| `type` | Reads | Fields | Credentials |
|---|---|---|---|
| `sqlite` | A SQLite file | `path` | None |
| `duckdb_file` | A DuckDB file | `path` | None |
| `postgres` | A PostgreSQL database | `connection` | `credentialsRef`: the password |
| `mysql` | A MySQL or MariaDB database | `connection` | `credentialsRef`: the password |
| `s3` | Files in an S3 bucket | `bucket`, `region`, `allowedPaths`, `endpoint`, `format` | `credentialsRef`: JSON with `key_id`, `secret`, `session_token` |
| `http` | Files behind HTTP URLs | `urls`, `format` | `headersRef`: JSON object of headers |
| `motherduck` | A MotherDuck database | `database` | `tokenRef`: the token |

Fields that end in `Ref` take a [secret reference](server-config.md). The files below are validated, but not tested against a real service.

=== "postgres"

    ```yaml
    --8<-- "examples/data-sources/policies.d/datasource-postgres.yaml:2"
    ```

=== "mysql"

    ```yaml
    --8<-- "examples/data-sources/policies.d/datasource-mysql.yaml:2"
    ```

=== "s3"

    ```yaml
    --8<-- "examples/data-sources/policies.d/datasource-s3.yaml:2"
    ```

=== "http"

    ```yaml
    --8<-- "examples/data-sources/policies.d/datasource-http.yaml:2"
    ```

=== "motherduck"

    ```yaml
    --8<-- "examples/data-sources/policies.d/datasource-motherduck.yaml:2"
    ```

- `postgres`, `mysql`: the URL needs a user and a database. The default ports are 5432 and 3306. `mysql` also accepts `mariadb://`.
- `s3`, `http`: each path or URL becomes one view in the schema `main`. Sluice reads nothing else.
- `s3`, `http`: the extension selects the format: `.parquet`, `.csv`, `.json`, `.ndjson`, `.jsonl` or `.xlsx`. `format` sets `parquet`, `csv`, `json` or `xlsx` for every path.
- `s3`: a path in `allowedPaths` can hold `*` or `**`, except for `xlsx`. `**` covers every directory below. `endpoint` names an S3-compatible store.
- `http`: no wildcards. Sluice sends the headers only to the hosts of `urls`.

## Check the files

```bash
--8<-- "examples/data-sources/steps/02-check.sh"
```

```text
--8<-- "examples/data-sources/expected/02-check.txt"
```

The command only reads the files. An invalid file ends it with status 1:

```bash
--8<-- "examples/data-sources/steps/03-check-invalid.sh"
```

```text
--8<-- "examples/data-sources/expected/03-check-invalid.txt"
```

A running server tests every source at start and then every 30 seconds. `GET /v1/ready` shows the result. See [Deployment](deployment.md).

## Limits

- Sluice attaches databases and files read-only. It reads `s3` and `http` data through views. No setting changes this.
- Sluice attaches sources only at start. Restart it after you add or change a `DataSource`.
- A source that fails a check of its type stops the start. Examples: a missing `sqlite` or `duckdb_file` file, `s3` without `region`, `motherduck` without `tokenRef`. `datasource check` and `config validate` miss these errors.
- `datasources.failFast: false` skips such a source, and `GET /v1/ready` does not list it. A source that Sluice cannot attach still stops the start: a database that is down, or a broken `duckdb_file` file.
- The health test does not read a `sqlite` file. A file that you remove later, or that is no database, stays healthy. Queries then fail with `ERR_INTERNAL`.

## See also

- [Server config & secrets](server-config.md)
- [Configuration reload](hot-reload.md)
- [Policy schema](../reference/policy-schema.md)
