<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Audit log

Sluice writes an audit record for every query decision. `sluice audit verify` checks the [hash chain](../reference/glossary.md) of the records.

## Example

Goal: read an audit record and verify the log.

```yaml
--8<-- "examples/audit-log/server.yaml:audit"
```

This query is allowed. A second query is denied.

```bash
--8<-- "examples/audit-log/steps/01-query.sh"
```

Find the record by its `meta` value, then verify:

```bash
--8<-- "examples/audit-log/steps/03-record.sh"
```

```json
--8<-- "examples/audit-log/expected/03-record.txt"
```

```bash
--8<-- "examples/audit-log/steps/04-verify.sh"
```

```text
--8<-- "examples/audit-log/expected/04-verify.txt"
```

Change one record in a copy:

```bash
--8<-- "examples/audit-log/steps/05-copy.sh"
--8<-- "examples/audit-log/steps/06-edit.sh"
--8<-- "examples/audit-log/steps/07-verify-copy.sh"
```

```text
--8<-- "examples/audit-log/expected/07-verify-copy.txt"
```

What happened: the changed record in line 4 no longer matches its hash.

## Record fields

| Field | Meaning |
|---|---|
| `timestamp`, `event_type`, `query_id` | Time, event, and the `X-Query-Id` response header |
| `subject` | `id`, `method` (`api_key` or `jwt`), `issuer`, `email`, `groups`, `remote_ip` |
| `origin`, `remote_ip` | `rest`, `mcp` or `admin`, and the IP address of the client |
| `sql_sample` | The SQL as sent, up to `audit.sqlSampleBytes`. 0 turns it off |
| `sql_fingerprint`, `rewritten_fingerprint` | Hashes of the sent and the rewritten SQL |
| `tables`, `catalogs` | What the query reads |
| `policies_applied` | The policies behind the decision |
| `policies_shadow` | Policies that matched in `Audit` or `DryRun` mode. `would: deny` or `would: reject` marks one that would have stopped the query. With the `composite` engine, also a column mask that lost to the mask of an earlier member |
| `decision`, `message` | `allow`, `deny`, `reject` or `error`, with the reason |
| `error_code` | Not on a policy `deny` or `reject`. A refused write is `error` with `ACL_REJECTED` |
| `row_count`, `truncated` | Result size, on `query-result` only. Left out when 0 or `false` |
| `duration_ms` | Time since the request started. Left out when 0. The examples leave it out |
| `client_meta` | The `meta` pairs of the request |
| `extras` | Other data, for example `approval_id`. A sample request sets `action: sample` and `sample` with `method`, `rows` or `rate`, `seed` and `stratify_by` |
| `sluice_version`, `parser_version` | The build. The parser, on `genesis` only |
| `prior_hash`, `hash` | The hash chain |

## Event types

| `event_type` | Written when |
|---|---|
| `genesis` | The audit directory is new |
| `query` | Sluice decides on a query |
| `query-result` | An allowed query ends |
| `admin-action` | `GET /admin/subjects/explain` or the MCP tool `explain_access` runs |
| `auth-event` | A request fails the login on REST, MCP over HTTP or the admin API |
| `approval-requested`, `approval-approved`, `approval-rejected`, `approval-expired` | An approval changes state |
| `synthetic-build` | A synthetic build in the server starts or ends. `sluice synth build` writes none |
| `audit-gap` | The audit file works again after a failed write |

An `auth-event` record has `origin`, `remote_ip`, the `error_code` `ERR_UNAUTHORIZED` and a `message`: `no credential`, `expired credential` or `invalid credential`. It holds no part of the credential. For each of the three listeners, Sluice writes at most 5 of these records per second, after a burst of 20. `sluice_audit_auth_events_dropped_total` counts the failed logins without a record.

A `synthetic-build` record has `origin` (`admin` or `startup`) and in `extras` the `phase` (`start` or `end`), `build_id`, `spec_hash` and `result`. On the end record, `content_hash`, `source_rows` and `synthetic_rows` hold the result. It holds no value of the source. The end `result` is `succeeded`, `failed` or `published_not_attached`. The last one means: the snapshot is current on disk, but the server could not attach it. The server serves it after the next start.

An `audit-gap` record has in `extras` the number `lost_records` and `last_written_hash`, the hash of the last record in the file before the gap. See [Write failures](#write-failures).

## Hash chain

Each `hash` is a SHA-256 over the record and `prior_hash`, the hash of the record before it. The first `prior_hash` is the [anchor](../reference/glossary.md): the SHA-256 of the [genesis](../reference/glossary.md) in `audit.file.genesis`. Pass it to verify as `--anchor`:

```bash
--8<-- "examples/audit-log/steps/08-verify-anchor.sh"
```

```text
--8<-- "examples/audit-log/expected/08-verify-anchor.txt"
```

| Exit status | Meaning |
|---|---|
| 0 | Chain intact |
| 1 | Directory or file not readable, or a keyed chain without `--key-ref` |
| 4 | Chain broken, a line that is not JSON, another anchor, a wrong key, or no record at all. Also `--key-ref` on a chain without a keyed record |
| 5 | Chain intact, but with `audit-gap` records. Verify prints a `gap:` line for each, `--json` a `gaps` list |

### Chain key

Without a key, anyone who can write the files can change a record and compute all later hashes again. Set `audit.chainKeyRef` to a secret of 32 bytes or more. Each new `hash` is then an HMAC-SHA256 with that key, and the record has `hash_alg: hmac-sha256`. Verify needs the same secret: `sluice audit verify <dir> --key-ref secret://env/SLUICE_AUDIT_CHAIN_KEY`.

You can add a key to an existing directory: the first keyed record follows the last record without a key. Sluice logs a warning at that start. After that the key must stay. Sluice does not start without it, or with another key, as a guard against a config mistake. With `--key-ref`, verify fails on a record without a key after a keyed one, and on a chain without any keyed record. So always verify a keyed chain with `--key-ref`. Keep the key away from the host that holds the log.

## Sinks

| Sink | Keys | Notes |
|---|---|---|
| File | `audit.file`: `path`, `rotateDaily`, `rotateSizeMB`, `genesis` | Holds the hash chain. A new `audit-YYYY-MM-DD.jsonl` starts each UTC day and at `rotateSizeMB` |
| Syslog | `audit.syslog`: `network`, `address`, `facility`, `tag` | RFC 5424, no TLS. UDP can lose messages. `tcp` and `unix` must connect when Sluice starts |
| S3 | `audit.s3`: `endpoint`, `bucket`, `prefix`, `region`, `credentialsRef`, `objectLock`, `retentionDays`, `uploadInterval`, `uploadBytes`, `maxBufferBytes`, `insecure`, `forcePathStyle` | Uploads batches of JSON lines. `objectLock` needs `retentionDays` and a bucket with Object Lock |

Syslog and S3 get copies. Their failures never stop a query. Each has its own queue of 10 000 records. A slow syslog or S3 sink does not hold up the file. See [Configuration](../reference/configuration.md).

- File: `audit.file.path` is required. `sluice serve` and `sluice mcp` do not start without it.
- File: a restart continues the same hash chain.
- File: Sluice creates the directory with mode `0700` and the files with mode `0640`.
- File: Sluice refuses a directory that is a symlink or that every user can write. On Windows, it checks only for a symlink.
- File: Sluice locks the directory with the file `.sluice.lock`. A second process with the same `audit.file.path` does not start. Give each process its own directory, or run `sluice serve` with `mcp.enabled` in place of a separate `sluice mcp`.
- File: a crash can leave a last line without a newline. At start, Sluice moves that line to `<file>.torn-<time>`, logs an error and continues the chain from the last full record.
- Sluice replaces invalid UTF-8 in a record with U+FFFD before it computes the hash. `sql_sample` and `client_meta` are cut at a character boundary.
- S3: objects are named `<prefix>/YYYY/MM/DD/audit-<id>.jsonl`.
- S3: an upload starts when the batch reaches `uploadBytes` or is older than `uploadInterval`. Sluice tries a failed upload again.
- S3: without `credentialsRef`, Sluice takes the credentials from the environment, the AWS credentials file or the IAM role.
- S3: when the buffer reaches `maxBufferBytes`, Sluice drops new records for this sink.
- Syslog: Sluice drops a record that it cannot send.
- Syslog and S3: when the queue of the sink is full, Sluice drops new records for this sink.
- `sluice_audit_dropped_total` counts the dropped records per `sink` and `reason`.

## Fail closed

`audit.failClosed: true` checks that the queue in memory takes the `query` record. The query fails with `ERR_AUDIT_UNAVAILABLE` in two cases:

- The queue is full. One request waits up to 200 ms for space. Other requests fail at once.
- The audit file cannot be written. See [Write failures](#write-failures).

With `false`, the query runs and `sluice_audit_dropped_total` counts the record. The check does not wait for the write to the file.

## Write failures

When a write to the audit file fails, Sluice opens the file again and tries once more. When that fails too:

1. Sluice logs an error. Each new record fails, so with `audit.failClosed: true` each query fails with `ERR_AUDIT_UNAVAILABLE`.
2. Records that were in the queue are lost.
3. Every second, Sluice tries to write an `audit-gap` record. When that works, new records work again. Sluice tries once more on shutdown.

The `audit-gap` record chains onto the lost records. `sluice audit verify` accepts the break only when `last_written_hash` is the hash of the record before it. It exits with status 5 and lists the gap. A removed record before the gap still breaks the chain.

## Limits

- No record for a reload, other admin calls, or a request over `limits.globalRps` or `limits.perIpRps`. In a flood of failed logins, only a part gets a record.
- Without `audit.chainKeyRef`, verify does not detect a change when the later hashes are computed again. With or without a key, it does not detect missing newest records or a replaced directory. The records before the first keyed one are only as safe as a chain without a key. Store each `last_hash` on another host. `--json` prints it in full.
- A failed disk write raises `sluice_audit_write_errors_total`. A record that the queue took is lost when its write fails. With `audit.failClosed: true` its query was already served.
- A failed fsync is logged and counted. It does not stop new records and writes no `audit-gap` record. When the system then crashes, records can be missing, and verify reports a broken chain.
- When Sluice stops or crashes before it writes the `audit-gap` record, the next start continues the chain from the last written record. The lost records then leave no trace in the chain.
- Sluice never deletes audit files. Remove whole files, oldest first. The rest verifies without `--anchor`.
- On shutdown, Sluice waits up to 10 seconds to write queued records.

## See also

- [Admin API](../reference/admin-api.md): `GET /admin/audit/tail` returns the newest records.
- [`sluice audit verify`](../reference/cli.md)
