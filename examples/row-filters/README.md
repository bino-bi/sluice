<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# row-filters

Four `RowFilterPolicy` objects on one `orders` table: a template value, nested checks, a CEL expression and a permissive filter.

## Run

```bash
examples/row-filters/serve.sh
```

The script creates `data/shop.db` from `seed.sql` and starts Sluice on `http://localhost:8080`. Then run the steps from this folder, for example `bash steps/01-ana.sh`.

| Step | Shows |
|---|---|
| `steps/01-ana.sh` | A sales rep reads only their own orders. |
| `steps/02-ben.sh` | A lead also reads all open orders. |
| `steps/03-refused-form.sh` | A SQL form that Sluice refuses on a filtered table. |
| `steps/04-form-to-write-instead.sh` | The same query in a form that works. |
| `steps/05-hex-number.sh` | A hex number is refused. |
| `steps/06-explain.sh` | `EXPLAIN` is refused on a filtered table. |
| `steps/07-two-part-name.sh` | A table name with two parts is rejected. |

| Suite | Shows |
|---|---|
| `tests/filters.yaml` | The rewritten SQL of each filter and of combined filters. |
| `tests/refused-forms.yaml` | Each refused SQL form, and the form to write instead. |

`expected/` holds the real output of each step. See [the examples guide](../README.md) for the layout.
