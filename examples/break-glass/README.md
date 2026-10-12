<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# break-glass

Analysts read `email` masked. A member of the `sre` group reads the real value. The audit log records who did.

## Run

```bash
examples/break-glass/serve.sh
```

The script creates `data/shop.db` from `seed.sql` and starts Sluice on `http://localhost:8080`. Then run the steps from this folder, for example `bash steps/01-analyst.sh`.

| Step | Shows |
|---|---|
| `steps/01-analyst.sh` | The analyst key reads `email` as `null`. |
| `steps/02-sre.sh` | The SRE key reads the real `email`. |
| `steps/03-audit-record.sh` | The audit record of the SRE query names the subject and its groups. |
| `steps/04-audit-verify.sh` | The audit log chain is intact. |

`expected/` holds the real output of each step. `tests/` holds a suite for `sluice policy test policies.d --tests tests`. See [the examples guide](../README.md) for the layout.

## Docker

```bash
python3 ../seed.py seed.sql data/shop.db
docker compose up
```

The compose file pulls the released image. `make docs-check` does not run it.
