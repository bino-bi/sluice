<!-- SPDX-License-Identifier: CC-BY-4.0 -->
# Examples

Every code block in the docs comes from a file in this folder. `make docs-check` runs every example and compares the output with stored files.

## Run an example by hand

You need a `sluice` binary on your `PATH` and `python3`.

```bash
make build && export PATH="$PWD/bin:$PATH"
examples/hello-sluice/serve.sh
```

The server listens on `http://localhost:8080`. The admin API listens on `http://localhost:9091`. Then run the commands in `steps/` from the example folder:

```bash
cd examples/hello-sluice
bash steps/01-query.sh
```

## Layout

| Path | Purpose |
|---|---|
| `policies.d/` | Policy objects, also `DataSource` and `SubjectBinding`. |
| `tests/*.yaml` | Suites for `sluice policy test policies.d --tests tests`. No server needed. |
| `server.yaml` | Server config. Paths are relative to the example folder. |
| `seed.sql` | Sample data. `serve.sh` loads it into `data/shop.db`. |
| `serve.sh` | Creates the sample data and starts Sluice. Only for examples that need a server. |
| `steps/NN-name.sh` | One command per file. The steps run in order, from the example folder. |
| `expected/NN-name.txt` | Output of each step from a real run. Never edit these by hand. |
| `manual/` | Files a reader uses by hand, for example a systemd unit. They get a syntax check only: `bash -n`, or a JSON, YAML or unit file parse. |
| `data/` | Created at run time. Not in git. `key-rotation` also writes hash files to `/tmp/sluice-key-rotation`, because a secret file needs an absolute path. |

An example needs `tests/` or `steps/`, or both. A file that nothing runs must not be shown in the docs.

## What the check does

For each example, `scripts/docs-examples.py`:

1. Deletes `data/`.
2. Runs `sluice policy test policies.d --tests tests` if `tests/` exists. All cases must pass.
3. Starts `serve.sh` if it exists and waits for `GET /v1/health`.
4. Runs each step with `bash`. It captures stdout, stderr and the exit status.
5. Compares the output with `expected/NN-name.txt`.

The output is changed in three ways before it is stored:

- JSON is indented for reading. This includes the `data:` lines of MCP answers.
- Values that differ between runs or builds are replaced with a placeholder such as `<query-id>`, `<time>`, `<hash>` or `<digest>`.
- The duration fields `duration_ms` and `latency_ms` are removed.

A step that ends with a non-zero status gets a last line `[exit status N]`.

To keep a value, list its placeholder in `expected/keep`. For example, `<hash>` keeps the output of `sluice apikey hash`.

## Add or change an example

```bash
make docs-update EXAMPLE=hello-sluice   # run it and rewrite expected/
git diff examples/hello-sluice/expected # read the new output
make docs-check                         # run everything
```

Rules:

- Server examples use ports 8080 (REST) and 9091 (admin). Some also use 8081 (MCP), 8090 and 8091 (helper processes). Only one example runs at a time.
- A step holds the command a reader types. Keep it short and without shell tricks.
- If the SQL holds single quotes, send the request body with a here-document (`-d @- <<'EOF'`). See `multi-tenant/steps/03-acme-asks-for-beta.sh`.
- Show rewritten SQL with a `rewrittenSql` or `rewrittenSqlContains` expectation in a test suite.
- Use `--8<-- [start:name]` and `--8<-- [end:name]` comments to mark a part of a file for the docs.
