<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Cross-source joins

One `SELECT` joins a table in a SQLite file with a table in a DuckDB file. Sluice checks access for each table.

## Example

Start the server with `examples/cross-source-join/serve.sh`. The script needs Go to write the DuckDB file.

Each data source is one catalog. Its name is the first part of `catalog.schema.table`.

```yaml
--8<-- "examples/cross-source-join/policies.d/datasource-shop.yaml:2"
```

```yaml
--8<-- "examples/cross-source-join/policies.d/datasource-ref.yaml:2"
```

The allow covers one table in each catalog.

```yaml
--8<-- "examples/cross-source-join/policies.d/allow-analytics.yaml:2"
```

The analyst key joins both tables.

```bash
--8<-- "examples/cross-source-join/steps/01-join.sh"
```

```json
--8<-- "examples/cross-source-join/expected/01-join.txt"
```

**What happened:** Sluice attached both files and ran one query over them. The allow covers both tables, so the join passes.

## More examples

The intern key may read `ref.main.countries` only. The same join is denied.

```bash
--8<-- "examples/cross-source-join/steps/02-join-denied.sh"
```

```json
--8<-- "examples/cross-source-join/expected/02-join-denied.txt"
```

A mask on `orders.customer_id` also applies inside the join.

```bash
--8<-- "examples/cross-source-join/steps/04-join-masked.sh"
```

```json
--8<-- "examples/cross-source-join/expected/04-join-masked.txt"
```

This suite case shows the rewritten SQL. The mask stays on the `orders` side.

```yaml
--8<-- "examples/cross-source-join/tests/join.yaml:masked-join"
```

A `QueryRejectPolicy` rejects a query on more than one catalog for selected subjects.

```yaml
--8<-- "examples/cross-source-join/policies.d/reject-two-catalogs.yaml:2"
```

This suite case proves it.

```yaml
--8<-- "examples/cross-source-join/tests/join.yaml:one-catalog"
```

The same join on Postgres and S3 is in `examples/cross-source-join/manual/postgres-s3/`. `make docs-check` only validates its policies.

## Limits

- `limits.disableCrossCatalog: true` in the server config rejects every query that names more than one catalog: `ACL_REJECTED`.

## See also

- [Data sources](../operations/data-sources.md)
- [Access control](../policies/access-control.md)
- [Guardrails](../policies/guardrails.md)
