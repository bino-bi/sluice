<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Data sources

Each `DataSource` object in the policies directory attaches one
catalog. The six MVP drivers all follow the same shape — type,
credentials, connection, optional readonly flag.

## Drivers

| Type          | Extension      | Credentials               | Notes                                            |
| ------------- | -------------- | ------------------------- | ------------------------------------------------ |
| `sqlite_file` | `sqlite`       | none                      | Zero-dep starter. Used by `hello-sluice`.        |
| `duckdb_file` | built-in       | none                      | Native ATTACH — no INSTALL/LOAD required.        |
| `postgres`    | `postgres`     | `credentialsRef` (URL)    | Empty ATTACH URL + named secret to hide creds.   |
| `mysql`       | `mysql`        | `credentialsRef`          | Accepts `mysql://` and `mariadb://`.             |
| `s3_parquet`  | `httpfs`       | JSON `{key_id, secret}`   | `allowedPaths` glob whitelist enforced.          |
| `motherduck`  | `motherduck`   | `tokenRef`                | `md:` prefix stripped automatically.             |

## Example

```yaml
apiVersion: sluice.bino-bi.github.io/v1
kind: DataSource
metadata:
  name: pg
spec:
  type: postgres
  connection: postgres://analytics@db.internal:5432/warehouse?sslmode=require
  credentialsRef: secret://file/etc/sluice/secrets/pg-password
  readonly: true
```

## Health

A background ticker probes each attached catalog. `sluice_build_info`
only flips to `ready=1` once every `DataSource` answers the health
query. Admin endpoint `GET /admin/datasources` exposes per-catalog
status, last error, and last schema pull time.

## Limits (MVP)

- All catalogs are attached **read-only**; writeable attach is on the
  v2 roadmap.
- `s3_parquet` views are created with `CREATE OR REPLACE VIEW`; wildcard
  expansion happens at query time via `read_parquet`.
- MotherDuck integration relies on a valid `tokenRef`; without a token
  the driver refuses to attach.
