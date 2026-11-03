<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Synthetic data

A `synthetic` data source serves generated rows that look like the rows of another data source. Use it to give developers and tests realistic data without real values.

Sluice reads the source once, builds a profile of each column, and writes generated rows to a snapshot file. Clients query the snapshot like any other catalog.

## Example

Goal: let developers query orders without access to the real orders.

The `shop` data source holds 2000 orders. `shopsynth` builds 20% of that from the profile of `shop`:

```yaml
--8<-- "examples/synthetic-data/policies.d/datasource-shopsynth.yaml:2"
```

The seed is a [secret reference](server-config.md) to at least 16 bytes. A `DataClassification` tags `email` as `pii`, so the rule replaces it with fake addresses:

```yaml
--8<-- "examples/synthetic-data/policies.d/classification.yaml:2"
```

The server config names the folder of the snapshots:

```yaml
--8<-- "examples/synthetic-data/server.yaml:synthetic"
```

Build the snapshot, then start the server:

```bash
--8<-- "examples/synthetic-data/serve.sh:build"
```

An access policy allows the group `developers` to read `shopsynth`. Another policy denies `shop`. A developer counts the orders per product:

```bash
--8<-- "examples/synthetic-data/steps/01-query-synthetic.sh"
```

```json
--8<-- "examples/synthetic-data/expected/01-query-synthetic.txt"
```

The same query on `shop` is denied:

```bash
--8<-- "examples/synthetic-data/steps/02-query-source-denied.sh"
```

```json
--8<-- "examples/synthetic-data/expected/02-query-source-denied.txt"
```

What happened: the build profiled `shop.main.orders` and wrote 400 generated rows. The product `gift card` has 2 source rows, below `minGroupSize`, so it never appears. The shares of the other products stay close to the source.

## Fields

The [policy schema](../reference/policy-schema.md#datasource) lists every field of `synthetic`. A table entry needs a `name` as `schema.table` and a `size`: either `ratio` or `rows`.

Each column gets one generator. Sluice takes the first match in this order:

1. The entry of the column under `tables[].columns`.
2. The first matching rule under `tables[].rules`.
3. The first matching rule under `synthetic.rules`.
4. The default for the column type.

A rule matches by `columns` (name patterns), `types` (type patterns such as `DECIMAL*`) and `tags`. All fields that are set must match.

| Column type | Default generator |
|---|---|
| Integers with at most 50 distinct values, each in at least `minGroupSize` rows | `categorical` |
| Other numbers, `TIME` | `distribution` |
| `BOOLEAN` | `boolean` |
| `ENUM` | `categorical` |
| `VARCHAR` with at most 50 distinct values, each in at least `minGroupSize` rows | `categorical` |
| Other `VARCHAR` | `randomString` with `length: source` |
| `DATE`, `TIMESTAMP` types | `dateDistribution` |
| `UUID` | `uuid` |
| Other types, such as `BLOB`, lists and structs | `null`, with a report warning |

A default column with fewer than `minGroupSize` non-null rows is written as `NULL`, with a report warning. A column entry for a column that the source table does not have fails the build.

## Generators

`source` means "from the profile of the source column". Every generator also takes `nullRate` and `unique`:

- `nullRate`: `source` (default) or a share from 0 to 1.
- `unique: true`: no value repeats in the column. The build fails when the generator runs out of values.

Number types are the integer types, `DECIMAL`, `FLOAT` and `DOUBLE`. Text is `VARCHAR`.

| Generator | Column types | Args, default in parentheses | Values |
|---|---|---|---|
| `distribution` | Numbers, `TIME` | `decimals`, `integer`, `smooth` (`true`) | Follows the quantiles of the source. |
| `uniform` | Numbers, `TIME` | `min`, `max` (source bounds) | Even spread between the bounds. |
| `normal` | Numbers | `truncate` (`true`), `decimals`, `integer` | Normal curve with the source mean and deviation. |
| `lognormal` | Numbers | `truncate` (`true`), `decimals`, `integer` | Log-normal curve. The source must hold only positive values. |
| `histogram` | Numbers | `bins` (20), `decimals`, `integer` | Equal-width bins with the source counts. |
| `bucket` | Numbers | `edges` or `buckets` (5) | Range labels such as `30-39`. Written as `VARCHAR`. |
| `constant` | Any except `BLOB`, lists and structs | `value`, required | One fixed value. |
| `sequence` | Integers, text | `start` (1), `step` (1) | 1, 2, 3 and so on. |
| `range` | Numbers | `min`, `max`, both required, `decimals` | Even spread between `min` and `max`. |
| `categorical` | Any except `BLOB`, lists and structs | `maxValues` (50), `otherLabel` | Source values with their shares. |
| `uniformCategory` | Any except `BLOB`, lists and structs | None | Source values, all with the same share. |
| `weighted` | Any except `BLOB`, lists and structs | `values`, required: a map of value to weight | Your values with your weights. |
| `boolean` | `BOOLEAN` | `trueRate` (`source`) | `true` with the given share. |
| `shuffleLabels` | Any except `BLOB`, lists and structs | `maxValues` (50) | Source values with the shares of other values. |
| `randomString` | Text | `length`, `initial`, `charset`, `case` | Random letters with the source lengths. |
| `pattern` | Text | `maxPatterns` (20) | Source shapes: `DE-4711` gives `QK-8302`. |
| `regex` | Text | `pattern`, required | Text that matches the pattern. |
| `fake` | Text | `kind`, required, `locale` (`en`), `keepInitial` | Fake names, emails, phone numbers, cities, IBANs and more. |
| `uuid` | `UUID`, text | `version` (4) | Random UUIDs, version 4 or 7. |
| `prefixKeep` | Text | `prefixLength`, required | Source prefixes, random rest. |
| `loremText` | Text | `words` (`source`), `vocabulary` (`lorem`) | Filler words with the source word counts. |
| `randomText` | Text | `length` (`source`), `vocabulary` (`lorem`) | Filler words cut to the source lengths. |
| `redact` | Text | `value` (`[redacted]`) | One fixed text. |
| `dateDistribution` | `DATE`, `TIMESTAMP` types | `granularity`, `keepWeekday`, `keepSeasonality` | Follows the dates of the source. |
| `dateRange` | `DATE`, `TIMESTAMP` types | `min`, `max` (source bounds), `granularity` | Even spread between the bounds. |
| `null` | Any | None | Always `NULL`. |

- `length` and `words` take `source`, a number, or `{min, max}`.
- `fake.kind` is one of `name`, `first_name`, `last_name`, `email`, `phone`, `company`, `city`, `country`, `street`, `iban`, `uuid`.
- `granularity` is one of `second`, `minute`, `hour`, `day`, `month`, `year`.
- Quote the keys `"Y"` and `"N"` in `weighted.values`. YAML reads them as booleans otherwise.
- A generator that does not fit the column type fails the build before Sluice profiles the column.

## Privacy settings

Set `privacy` on the data source, on a table, or on a column entry. The most specific level wins.

| Setting | Default | What it does |
|---|---|---|
| `minGroupSize` | 5 | A value, length, bin or bound must come from at least this many source rows. Rarer values never appear. |
| `clipOutliers` | Off | Cuts the source below the `lower` and above the `upper` quantile before the profile. |
| `extremeJitter` | `{min: 0.01, max: 0.02}` | Moves the lowest and highest value by a random share of the range. `off` turns it off. |
| `spikeWarning` | `true` | Warns when fewer than `minGroupSize` rows lie in the lowest or highest 1% of a number or date range. |
| `strictTags` | `[pii]` | A column with one of these tags needs a column entry or a rule. The default generator is not enough. |

- Columns with fewer than `minGroupSize` non-null rows fail the build, unless the generator needs no profile. Examples: `constant`, `sequence`, `range`, `weighted`, `regex`, `uuid`, `redact`, `null`.
- A spike goes to the build log, the report and the metric `sluice_synthetic_spikes`. The build does not fail. Set `clipOutliers` to remove it.
- `strictTags` is set only on the data source. Tags come from `DataClassification` objects on the source catalog.

## Build and publish

Three things start a build:

- `sluice synth build <name>` reads the server config and the policy directory. It needs no running server. See the [CLI reference](../reference/cli.md#synth-build).
- `POST /admin/synthetic/<name>/build` on the [admin API](../reference/admin-api.md#synthetic-builds) builds in the running server.
- `build.onStart: ifMissing` builds at server start when no snapshot exists. The server serves queries during the build.

The size check runs before the profile. A `ratio` above `synthetic.maxRatio` of the server config fails the build, and so does a `rows` count above that share.

A build writes into `synthetic.dir`:

| Path | Content |
|---|---|
| `<name>/snapshots/<build-id>.duckdb` | The generated rows. |
| `<name>/reports/<build-id>.json` | The report. Mode `0600`. |
| `<name>/current` | The build id that Sluice serves. |

Sluice changes `current` only after every check passed. A failed build writes its report and leaves `current` as it was. Sluice keeps the current and the previous snapshot and deletes older ones. Clients can read only the `snapshots` folder.

The same spec, seed and source data give the same rows on the same CPU architecture. The report holds a `content_hash` of the rows to compare builds.

## Status

`GET /admin/datasources` shows a `status` for each synthetic data source:

| Status | Meaning |
|---|---|
| `not_built` | No snapshot exists. The catalog has no tables. |
| `ready` | The snapshot was built from the current spec. |
| `stale` | The snapshot was built from another spec. Sluice still serves it, logs a warning and sets `sluice_synthetic_stale` to 1. |

```bash
--8<-- "examples/synthetic-data/steps/03-datasources.sh"
```

```json
--8<-- "examples/synthetic-data/expected/03-datasources.txt"
```

## Report

`sluice synth report` prints the report of the current build. `--json` prints the whole file, and `GET /admin/synthetic/<name>` returns it too.

```bash
--8<-- "examples/synthetic-data/steps/04-report.sh"
```

```text
--8<-- "examples/synthetic-data/expected/04-report.txt"
```

- `DISTANCE` compares source and synthetic data. `ks` is the largest gap between the two distributions. `tv` is the share of rows that would need another value.
- Medians of dates are in days since 1970-01-01. Medians of timestamps are in seconds.
- The source `min` and `max` in the JSON are not the real extremes. They hold the `minGroupSize`-th value from each end, or the `clipOutliers` bounds.
- The report holds no source rows, but it holds counts and statistics of the source. Keep it private.

## Limits

- The privacy settings make rare values and extremes hard to find. They are no formal privacy guarantee.
- `sluice synth build` writes no record to the audit log. It prints the record on stderr and stores it in the report.
- A running server picks up a snapshot from `sluice synth build` only after a restart. Use the admin API to build in the server.
- Each build keeps only the current and the previous snapshot. Two CLI builds against a running server delete the file it serves. Restart the server after CLI builds into its folder.
- Sluice attaches data sources only at start. Restart it after you add a synthetic data source or change its spec.
- The seed value is not part of the `stale` check. Build again after you change the seed value.

## See also

- [Data sources](data-sources.md)
- [Classification tags](../policies/classification.md)
- [Policy schema](../reference/policy-schema.md#datasource)
- [Admin API](../reference/admin-api.md#synthetic-builds)
