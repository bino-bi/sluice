<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Synthetic data

A `synthetic` data source serves generated rows that look like the rows of another data source. Use it to give developers and tests realistic data without real values.

Sluice reads the source once, builds a profile of each column, and writes generated rows to a snapshot file. Clients query the snapshot like any other catalog.

## Example

Goal: let developers query customers and orders without access to the real rows.

The `shop` data source holds 400 customers and 2000 orders. `shopsynth` builds 20% of each table from the profile of `shop`:

```yaml
--8<-- "examples/synthetic-data/policies.d/datasource-shopsynth.yaml:2"
```

- `customer_id` takes the keys of the synthetic customers. Each customer gets about as many orders as in the source.
- `amount` and `quantity` keep their rank correlation. See [Correlated columns](#correlated-columns).
- `placed_on` follows the dates of the same product in the source.
- `shipped_on` is `placed_on` plus a gap like the source gaps.
- `email` is built from the synthetic names of the same row.
- No synthetic customer has the first and last name of a source customer.

The seed is a [secret reference](server-config.md) to at least 16 bytes. A `DataClassification` tags `email` as `pii`. A tagged column needs a column entry or a rule, so the `template` entry is required:

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

What happened: the build profiled `shop.main.customers` and `shop.main.orders` and wrote 80 customers and 400 orders. The product `gift card` has 2 source rows, below `minGroupSize`, so it never appears. The shares of the other products stay close to the source.

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

- `nullRate`: `source` (default) or a share from 0 to 1. For `template` and `expression` the default is 0.
- `unique: true`: no value repeats in the column. The build fails when the generator runs out of values. `template`, `expression`, `foreignKey` and `correlated` do not take it.

Number types are the integer types, `DECIMAL`, `FLOAT` and `DOUBLE`. Text is `VARCHAR`.

| Generator | Column types | Args, default in parentheses | Values |
|---|---|---|---|
| `distribution` | Numbers, `TIME` | `decimals`, `integer`, `smooth` (`true`) | Follows the quantiles of the source. |
| `correlated` | Numbers, `TIME` | `with`, `decimals`, `integer`, `smooth` (`true`) | Keeps the rank correlation between the columns of a group. See [Correlated columns](#correlated-columns). |
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
| `foreignKey` | Integers, text, `UUID` | `table` and `column`, both required, `fanOut` (`source`), `orphanRate` (0) | Keys of a parent table. See [Foreign keys](#foreign-keys). |
| `dateOffset` | `DATE`, `TIMESTAMP` types | `from`, required, `offset` (`source`) | The value of `from` plus a gap like the source gaps. |
| `template` | Text | `template`, required, `lower` (`false`) | Text from other columns of the row, such as `{first_name}.{last_name}`. |
| `expression` | Any except `BLOB`, `INTERVAL`, lists and structs | `sql`, required | A SQL expression over other columns of the row. See [Expressions](#expressions). |
| `sourceVocabulary` | Text | `minWordCount` (20), `words` (`source`), `maxWords` (1000) | Frequent source words with their shares. |
| `sentenceShape` | Text | `vocabulary` (`lorem`), `minWordCount`, `maxWords` | Sentences with the source counts, lengths and end marks. |

- `distribution`, `categorical` and `dateDistribution` also take `by`. See [Groups with by](#groups-with-by).
- `length` and `words` take `source`, a number, or `{min, max}`.
- `fanOut` takes `source` or `{min, max}`: the number of child rows per parent, from 0 to 100000.
- `orphanRate` takes `source` or a share from 0 to 1. It is the share of child rows with a key that has no parent.
- `offset` takes `source` or `{min, max}` in days, from -36500 to 36500.
- In a `template`, `{name}` inserts a column of the row. Write `{{` and `}}` for a brace. `lower: true` writes the result in lower case.
- `sourceVocabulary` keeps a word only when it occurs `minWordCount` times and in `minGroupSize` rows. A word is a run of letters, so digits never appear. `maxWords` is the size of the word list.
- `sentenceShape.vocabulary` is one of `lorem`, `en`, `de`, `source`. `minWordCount` and `maxWords` work only with `source`.
- `fake.kind` is one of `name`, `first_name`, `last_name`, `email`, `phone`, `company`, `city`, `country`, `street`, `iban`, `uuid`.
- `granularity` is one of `second`, `minute`, `hour`, `day`, `month`, `year`.
- Quote the keys `"Y"` and `"N"` in `weighted.values`. YAML reads them as booleans otherwise.
- A generator that does not fit the column type fails the build before Sluice profiles the column.

## Correlated columns

`correlated` keeps the rank correlation between number columns of one table. In the example source, a larger `amount` comes with a larger `quantity`:

```yaml
--8<-- "examples/synthetic-data/policies.d/datasource-shopsynth.yaml:correlated"
```

```bash
--8<-- "examples/synthetic-data/steps/06-correlation.sh"
```

```json
--8<-- "examples/synthetic-data/expected/06-correlation.txt"
```

What happened: each column got its own values, as with `distribution`. The group only decided which values meet in one row.

- `with` names 1 to 7 other `correlated` columns of the same table entry. One side is enough: `quantity` needs no `with`.
- Columns that `with` links form a group of 2 to 8 columns.
- Sluice measures the rank correlation (Spearman) of each pair from a table of up to 20 by 20 cells.
- A pair needs `2 x minGroupSize` rows where both values are set. Fewer rows fail the build.
- A pair without spread, such as a constant column, gets the correlation 0 and a report warning.
- When the measured values do not fit together, Sluice moves them toward 0 until they do. The report shows this share as `shrinkage`.
- `correlated` is set only under `tables[].columns`, not in a rule. `DATE` and `TIMESTAMP` columns are refused.
- `correlated` does not take `unique`, and `rejectExactMatches` cannot name it. Sluice cannot draw one row of a group again.

## Privacy settings

Set `privacy` on the data source, on a table, or on a column entry. The most specific level wins.

| Setting | Default | What it does |
|---|---|---|
| `minGroupSize` | 5 | A value, length, bin or bound must come from at least this many source rows. Rarer values never appear. |
| `clipOutliers` | Off | Cuts the source below the `lower` and above the `upper` quantile before the profile. |
| `extremeJitter` | `{min: 0.01, max: 0.02}` | Moves the lowest and highest value by a random share of the range. `off` turns it off. |
| `spikeWarning` | `true` | Warns when fewer than `minGroupSize` rows lie in the lowest or highest 1% of a number or date range. |
| `strictTags` | `[pii]` | A column with one of these tags needs a column entry or a rule. The default generator is not enough. |
| `rejectExactMatches` | Off | Columns that together must not equal a source row. Sluice draws matching rows again. |
| `noise` | Off | Adds random noise to the counts of the profile. Takes `epsilon` from 0.001 to 100. See [Noise](#noise). |
| `bounds` | None | The public `min` and `max` of a number, `TIME` or date column. Needs `noise`. |

- Columns with fewer than `minGroupSize` non-null rows fail the build, unless the generator needs no profile. Examples: `constant`, `sequence`, `range`, `weighted`, `regex`, `uuid`, `redact`, `null`.
- A spike goes to the build log, the report and the metric `sluice_synthetic_spikes`. The build does not fail. Set `clipOutliers` to remove it.
- `strictTags` is set only on the data source. Tags come from `DataClassification` objects on the source catalog.
- `rejectExactMatches` is set only on a table. It takes 1 to 32 column names.
- `noise` is set only on the data source. `bounds` is set only on a column entry.

`rejectExactMatches` compares the listed columns of each synthetic row with every source row:

- Values compare in the snapshot types. `NULL` equals `NULL`.
- The build fails when more than 1% of the rows match. Then the columns are too predictable together.
- Otherwise Sluice draws the listed columns of the matching rows again, and the columns that read them. It checks again, up to 10 rounds. Rows that still match fail the build.
- Source rows stay inside DuckDB. Errors and the report hold only counts and column names.

## Noise

`noise` adds random noise (Laplace noise) to the counts that Sluice reads from the source. One source row then changes the snapshot only a little. A smaller `epsilon` means more noise.

`shopnoisy` builds the orders of the example with noise. `amount` gets public bounds:

```yaml
--8<-- "examples/synthetic-data/policies.d/datasource-shopnoisy.yaml:2"
```

```bash
--8<-- "examples/synthetic-data/steps/09-report-noisy.sh"
```

```text
--8<-- "examples/synthetic-data/expected/09-report-noisy.txt"
```

```bash
--8<-- "examples/synthetic-data/steps/08-noisy-range.sh"
```

```json
--8<-- "examples/synthetic-data/expected/08-noisy-range.txt"
```

What happened: the build made 22 noisy measurements, so each got an `epsilon` of 10 / 22. The range of `amount` comes from its bounds and the kept bins. The other number and date columns warn that their range has no noise.

### Budget

- `epsilon` is the budget of one build of the whole data source. Sluice splits it evenly over all noisy measurements of the build.
- A measurement is one released set of counts. Examples: the rows of a table, the nulls of a column, one histogram, one correlated pair.
- A rebuild over changed data spends `epsilon` again. Nothing adds up the budget over builds.
- Sluice counts the measurements before it reads data. A default with a fallback counts every generator it may fall back to.
- `foreignKey`, `dateOffset`, `sourceVocabulary`, `sentenceShape` and `by` are refused with `noise`.

### Counts

- Each count is the true count plus noise, rounded, and at least 0.
- A histogram cell, such as a bin or a weekday, is kept when it reaches `minGroupSize` and a floor that grows with the noise.
- A value, initial, pattern or prefix is kept when its noisy count reaches `minGroupSize`.
- Quantiles, mean and deviation come from a noisy histogram of 200 bins, or of `bins` for `histogram`.
- The same data, seed, spec and DuckDB version give the same rows. Any change in the source changes the noise of every count.

### Min and max

- With `bounds`, Sluice cuts the values to the bounds and splits that range into equal bins. The output range is the outer edges of the kept bins.
- A lone far value cannot keep its bin, so it does not show in the range.
- Set `bounds` close to the real range. A bin is (max - min) / 200 wide, so wide bounds put all rows in a few bins.
- On small tables use `histogram` with fewer `bins`. The example has 2000 rows and needs `epsilon: 10` for usable numbers.
- Without `bounds`, the range comes from the profile, as without noise. The report marks the column `range: profile` and warns.
- `bounds` takes numbers for number columns and seconds of the day for `TIME`. For `DATE` and `TIMESTAMP` columns it takes ISO dates or timestamps.
- `bounds` on a generator that reads no numbers, such as `categorical`, fails the build.
- A very small `epsilon` can fail the build for a number column. It can also give the column a narrow random range.

### Not covered by noise

Do not call a snapshot differentially private without this list. Noise does not cover:

- Which values, initials, patterns and prefixes appear. `minGroupSize` protects them, as without noise.
- The range of number and date columns without `bounds`, and spike warnings without `bounds`.
- The report and the audit record. They hold exact counts of the source.
- Whether the source changed between two builds.
- The rows that `rejectExactMatches` compares with the source.
- Attacks on the rounding of floating-point numbers in the noise.
- A known seed. Anyone with the seed and the spec can remove the noise.

## Relations

`foreignKey`, `dateOffset`, `template`, `expression` and `by` read other columns. Set them only under `tables[].columns`. A rule matches columns in many tables, so a column name in its args could point to a different column in each.

Every synthetic order of the example points to a synthetic customer. Count the orders per city through a join:

```bash
--8<-- "examples/synthetic-data/steps/05-join.sh"
```

```json
--8<-- "examples/synthetic-data/expected/05-join.txt"
```

What happened: all 400 orders find their customer, so no row has the city `NULL`.

### Order

- Sluice builds a parent table before the tables that point to it. Other tables keep the list order. The report lists the tables in build order.
- Inside a row, Sluice draws a column after the columns it reads.
- A `foreignKey` to its own table is refused. Tables or columns that depend on each other in a cycle are refused.
- `sluice config validate` finds these errors. A column name that the source table does not have fails the build.

### Foreign keys

- The parent column needs unique values: the generator `sequence` or `uuid`, or `unique: true`.
- Parent and child column hold the same kind of key: both integers, or both text or `UUID`.
- `fanOut: source` keeps the fan-out of the source: the number of child rows per parent. A fan-out that fewer than `minGroupSize` parents share is merged into a near one.
- When the table sizes do not fit the source fan-out, Sluice scales it. The report warns when the scale is below 0.5 or above 2.
- Orphan keys are new values, not source keys.
- After the write, Sluice checks that every key except the orphans finds its parent. The snapshot has no `FOREIGN KEY` constraint.
- Sluice keeps the parent keys in memory during the build, about 50 bytes per parent row.

### Groups with by

`by` names 1 to 3 columns. The generator then uses the profile of the rows that have the same values in these columns. The example uses the dates of the same product.

- A `by` column is text, `ENUM`, `BOOLEAN`, `DATE` or an integer type up to `BIGINT`.
- Values compare as text. The `by` column needs a generator that keeps source values, such as `categorical`. The report warns when most rows find no group. `bucket` on a `by` column is refused.
- A group needs `minGroupSize` rows. Sluice uses the 100 largest groups. Other rows use the profile of the whole column.
- `NULL` is a group value of its own.
- The spike check runs per group. The report counts groups with a spike, but names none.

### Expressions

`expression` computes a column with a DuckDB SQL expression over other columns of the same row, for example `quantity * unit_price`. DuckDB computes it when Sluice writes the snapshot, in the snapshot types.

- Allowed: constants, columns, operators, `CASE`, `CAST` and scalar functions that give the same result each time.
- Refused: subqueries, table names, aggregates, window functions, table functions, settings, and functions such as `random()` or `now()`.
- An expression cannot read another `expression` column. `template`, `dateOffset`, `by` and `rejectExactMatches` cannot name one either.
- `sql` holds at most 1024 bytes, without `;`, comments or line breaks. `sluice config validate` checks only this. The full check runs at build start, before Sluice reads the source.

A `template` is `NULL` when a column it reads is `NULL`. A `dateOffset` is `NULL` when `from` is `NULL`. With `offset: source`, the gap keeps its sign: when no source gap is negative, no synthetic gap is either.

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

The same spec, seed and source data give the same rows on the same CPU architecture. With `noise`, the DuckDB version must be the same too. The report holds a `content_hash` of the rows to compare builds.

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
- For `foreignKey`, `tv` compares the fan-outs. For `dateOffset`, `ks` compares the gaps to `from`.
- Medians of dates are in days since 1970-01-01. Medians of timestamps are in seconds.
- A spike in a `by` group gives one warning line with the number of such groups.
- `sluice synth report` prints a `noise` line with the budget, and a `correlated` line for each group.
- The source `min` and `max` in the JSON are not the real extremes. They hold the `minGroupSize`-th value from each end, or the `clipOutliers` bounds.
- The report holds no source rows, but it holds counts and statistics of the source. Keep it private.

The JSON has these fields for relations, correlated columns and noise. Each one is left out when it does not apply.

| Field | Meaning |
|---|---|
| `tables[].depends_on` | The parent tables of the `foreignKey` columns. |
| `tables[].exact_matches` | `columns`, `matched_rows` in the first round, `redrawn_rows` in all rounds, and `rounds`. |
| `columns[].depends_on` | The columns that this column reads. |
| `columns[].by` | `columns`, `groups`, `dropped_groups`, `fallback_rows` that used the profile of the whole column, and `spike_groups`. |
| `columns[].fan_out` | The `parent` column, the number of `parents`, the `scale` and the `orphan_rows`. |
| `columns[].words` | The number of kept words of `sourceVocabulary` or `sentenceShape`. |
| `noise` | `epsilon`, the number of `measurements` and `epsilon_per_measurement`. |
| `tables[].correlations` | Per group: `columns`, `cells` per column, `shrinkage`, the `source` and `synthetic` rank correlations, and `max_diff`. |
| `columns[].noise_measurements` | The noisy measurements of the column. |
| `columns[].range` | With noise: `bounds` or `profile`, the origin of the min and max. |

`sluice synth build` also prints one line for each `rejectExactMatches` table and each `foreignKey` column.

## Limits

- Without `noise` the settings give no formal privacy guarantee. With `noise`, `epsilon` covers the counts listed in [Noise](#noise), not which labels appear and not ranges without `bounds`.
- `rejectExactMatches` stops exact copies, not near copies. `sourceVocabulary` keeps a name that many rows hold. `by` uses at most 100 groups.
- `sluice synth build` writes no record to the audit log. It prints the record on stderr and stores it in the report.
- A running server picks up a snapshot from `sluice synth build` only after a restart. Each build keeps only the current and the previous snapshot, so two CLI builds delete the file a running server serves. Use the admin API to build in the server.
- Sluice attaches data sources only at start. Restart it after you add a synthetic data source or change its spec. The seed value is not part of the `stale` check, so build again after you change it.

## See also

- [Data sources](data-sources.md)
- [Classification tags](../policies/classification.md)
- [Policy schema](../reference/policy-schema.md#datasource)
- [Admin API](../reference/admin-api.md#synthetic-builds)
