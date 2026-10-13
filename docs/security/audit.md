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
| `policies_shadow` | Policies that matched in `Audit` or `DryRun` mode. `would: deny` or `would: reject` marks one that would have stopped the query |
| `decision`, `message` | `allow`, `deny`, `reject` or `error`, with the reason |
| `error_code` | Not on a policy `deny` or `reject`. A refused write is `error` with `ACL_REJECTED` |
| `row_count`, `truncated` | Result size, on `query-result` only. Left out when 0 or `false` |
| `duration_ms` | Time since the request started. Left out when 0. The examples leave it out |
| `client_meta` | The `meta` pairs of the request |
| `extras` | Other data, for example `approval_id` |
| `sluice_version`, `parser_version` | The build. The parser, on `genesis` only |
| `prior_hash`, `hash` | The hash chain |

## Event types

| `event_type` | Written when |
|---|---|
| `genesis` | The audit directory is new |
| `query` | Sluice decides on a query |
| `query-result` | An allowed query ends |
| `admin-action` | `GET /admin/subjects/explain` or the MCP tool `explain_access` runs |
| `approval-requested`, `approval-approved`, `approval-rejected`, `approval-expired` | An approval changes state |

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
| 1 | Directory or file not readable |
| 4 | Chain broken, a line that is not JSON, or another anchor |

## Sinks

| Sink | Keys | Notes |
|---|---|---|
| File | `audit.file`: `path`, `rotateDaily`, `rotateSizeMB`, `genesis` | Holds the hash chain. A new `audit-YYYY-MM-DD.jsonl` starts each UTC day and at `rotateSizeMB` |
| Syslog | `audit.syslog`: `network`, `address`, `facility`, `tag` | RFC 5424, no TLS. UDP can lose messages. `tcp` and `unix` must connect when Sluice starts |
| S3 | `audit.s3`: `endpoint`, `bucket`, `prefix`, `region`, `credentialsRef`, `objectLock`, `retentionDays`, `uploadInterval`, `uploadBytes`, `maxBufferBytes`, `insecure`, `forcePathStyle` | Uploads batches of JSON lines. `objectLock` needs `retentionDays` and a bucket with Object Lock |

Syslog and S3 get copies. Their failures never stop a query. See [Configuration](../reference/configuration.md).

- File: a restart continues the same hash chain.
- File: Sluice creates the directory with mode `0750` and the files with mode `0640`.
- S3: objects are named `<prefix>/YYYY/MM/DD/audit-<id>.jsonl`.
- S3: an upload starts when the batch reaches `uploadBytes` or is older than `uploadInterval`. Sluice tries a failed upload again.
- S3: without `credentialsRef`, Sluice takes the credentials from the environment, the AWS credentials file or the IAM role.
- S3: when the buffer reaches `maxBufferBytes`, Sluice drops new records for this sink.
- Syslog: Sluice drops a record that it cannot send.
- `sluice_audit_dropped_total` counts the dropped records per `sink`.

## Fail closed

`audit.failClosed: true` checks one thing: the queue in memory takes the `query` record within 200 ms. If not, the query fails with `ERR_AUDIT_UNAVAILABLE`. With `false`, the query runs and `sluice_audit_dropped_total` counts the record.

## Limits

- No record for a failed login, a reload, other admin calls, or a request over `limits.globalRps` or `limits.perIpRps`.
- The hash has no secret key. Verify does not detect missing newest records, or a change when the later hashes are computed again. Store each `last_hash` on another host. `--json` prints it in full. A changed log no longer contains it.
- A failed disk write does not stop queries. It raises `sluice_audit_write_errors_total`.
- Sluice never deletes audit files. Remove whole files, oldest first. The rest verifies without `--anchor`.
- On shutdown, Sluice waits up to 10 seconds to write queued records.

## See also

- [Admin API](../reference/admin-api.md): `GET /admin/audit/tail` returns the newest records.
- [`sluice audit verify`](../reference/cli.md)
