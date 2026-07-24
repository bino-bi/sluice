<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Audit trail

Every terminal outcome appends at least one record to a hash-chained JSONL
log: deny, reject, and error each write one; an allowed query writes a
fail-closed access record (`query`) before the first row and a `query-result`
completion record with `row_count` / `truncated` when the stream closes. The
chain makes tampering detectable, and the default fail-closed gate makes the
log a precondition for serving data, not an afterthought.

## What a record contains

| Field | Meaning |
|---|---|
| `timestamp`, `event_type`, `query_id` | When, what kind of event, and the id echoed to the client as `X-Query-Id` |
| `subject` | `id`, `method` (jwt / api_key / admin_token / none), `issuer`, `email`, `groups`, `remote_ip` |
| `origin` | Transport that carried the request: `rest`, `mcp`, or `admin` |
| `sql_fingerprint`, `rewritten_fingerprint`, `sql_sample` | Fingerprints of the inbound and rewritten SQL, plus a bounded sample (`audit.sqlSampleBytes`, default 2048; `0` disables) |
| `tables`, `catalogs` | Resources the statement touched |
| `policies_applied`, `decision`, `error_code` | Which policies fired, the outcome (`allow`, `deny`, `reject`, `error`), and the error code if any |
| `row_count`, `truncated`, `duration_ms` | Result metadata, only on the `query-result` record written when the stream closes |
| `client_meta` | The caller's `meta` request pairs, capped server-side (16 keys, 64-byte keys, 256-byte values). Recorded verbatim inside the operator trust domain, like `sql_sample` — clients should not put secrets in `meta` |
| `sluice_version`, `duckdb_version`, `parser_version` | Build provenance |
| `prior_hash`, `hash` | The chain links (see below) |

Event types: `query`, `query-result`, `policy-decision`, `config-reload`,
`auth-event`, `admin-action`, `datasource-connect`, `genesis`, and the
approval lifecycle events `approval-requested` / `approval-approved` /
`approval-rejected` / `approval-expired`.

## The hash chain

Each record's hash is `sha256(prior_hash + "\n" + canonical JSON of the
record without its hash field)`; that hash becomes the next record's
`prior_hash`. The first record is anchored on `sha256(seed)`, where the seed
comes from the `audit.file.genesis` `secret://` reference in `sluice.yaml`.
Changing any field of any record breaks every hash after it. (Verification
canonicalizes each line before hashing, so pure formatting changes — key
order, whitespace — are tolerated by design.)

```yaml
# sluice.yaml
audit:
  failClosed: true
  file:
    path: /var/lib/sluice/audit
    rotateDaily: true
    rotateSizeMB: 256
    genesis: secret://file//etc/sluice/audit-genesis
```

!!! warning "Pin the genesis seed"
    Without `audit.file.genesis`, Sluice derives the seed from the hostname
    and build commit. That fallback changes when the host or binary changes,
    so anchored verification breaks across redeploys. Always configure a
    stable seed (for example `openssl rand -hex 32` stored as a secret).

## The file sink

Records are appended to `audit-YYYY-MM-DD.jsonl` in the configured directory
(created `0750`, files `0640`), rotating at midnight UTC and additionally at
`rotateSizeMB` (`audit-YYYY-MM-DD-1.jsonl`, `-2`, ...). On restart, Sluice
reads the tail of the newest file and continues the chain where it stopped.

!!! warning "No sink configured means a temp directory"
    If `audit.file.path` is unset, Sluice still boots but writes to
    `$TMPDIR/sluice-audit` with a loud warning. Temp directories get cleaned;
    set a persistent path in production.

## Fail-closed serving

With `audit.failClosed: true` (the default), the query pipeline refuses to
stream the first row until the access record has been durably enqueued. If
the audit dispatcher cannot accept it, the client gets `ERR_AUDIT_UNAVAILABLE`
and no data.

!!! danger "Do not turn this off casually"
    `failClosed: false` trades integrity for availability: queries keep
    flowing while records drop (counted in `sluice_audit_dropped_total`).
    For a gateway whose purpose is provable access control, an unaudited
    query is usually worse than a failed one.

The dispatcher is bounded: a 10,000-record queue and a 200 ms enqueue
deadline, so a stuck disk degrades into fast, visible failures rather than
unbounded memory growth. Shutdown drains the queue for up to 10 seconds.
Alert on `sluice_audit_queue_depth`, `sluice_audit_dropped_total`, and
`sluice_audit_write_errors_total` — see [Observability](../operations/observability.md).

## Verifying the chain

```console
$ sluice audit verify /var/lib/sluice/audit --anchor "$(printf '%s' "$SEED" | shasum -a 256 | cut -d' ' -f1)"
chain OK (3 file(s), 41287 record(s), last_hash=9f31c2ab54e0d871…)
```

`verify` walks every `.jsonl` file in date order, recomputes each hash, and
carries the prior hash across file boundaries. `--anchor` pins the first
record's `prior_hash` to `sha256(seed)` so an attacker cannot splice in a
chain from a different installation. Exit codes: `0` intact, `1` I/O error,
`4` chain broken (with the offending file and line).

If verification fails: treat it as an incident, not a glitch. Preserve the
directory read-only, identify the first broken line from the error, compare
against your offsite copy, and review who had filesystem access. Records
*before* the break are still trustworthy; everything after it is not.

## Reading the log

Tail recent records through the admin API:

```bash
curl -s -H "Authorization: Bearer $SLUICE_ADMIN_TOKEN" \
  "http://127.0.0.1:9091/admin/audit/tail?n=50"
```

Who touched a specific table, straight from the files:

```bash
jq -r 'select((.tables // []) | index("shop.main.customers"))
       | [.timestamp, .subject.id, .decision, .error_code // ""] | @tsv' \
  /var/lib/sluice/audit/audit-*.jsonl
```

## Declaring sinks in YAML

An `AuditSink` resource can live alongside your policies to record sink
intent; it parses and validates like any other kind:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: AuditSink
metadata:
  name: audit-file
spec:
  type: file
  path: /var/lib/sluice/audit
  rotateDaily: true
  rotateSizeMB: 256
```

!!! warning "Manifests declare the file sink only"
    The running sinks are wired from the `audit.*` block in `sluice.yaml`,
    not from manifests. AuditSink manifests declaring `s3` or `syslog`
    **fail validation** with a pointer to the server-config keys below;
    `postgres` and `otlp` remain unimplemented and fail validation too
    (`sluice config validate` exits 3, `sluice serve` refuses to start).

## Secondary sinks: syslog and S3

Beyond the file sink, two network sinks fan out every record:

```yaml
audit:
  file:
    path: /var/lib/sluice/audit        # the durable, hash-chained record
  syslog:
    network: tcp                       # udp (default) | tcp | unix | unixgram
    address: siem.internal:6514
    facility: local0
    tag: sluice
  s3:
    endpoint: s3.amazonaws.com         # or a MinIO endpoint
    bucket: acme-sluice-audit
    prefix: audit/
    region: eu-central-1
    objectLock: compliance             # "" | governance | compliance
    retentionDays: 365
    credentialsRef: secret://env/SLUICE_S3_CREDS   # JSON {accessKeyId, secretAccessKey}
    uploadInterval: 30s
    uploadBytes: 1048576
```

Their delivery semantics are deliberately **best-effort**:

- The **file sink stays the durable record** and carries the hash chain;
  `audit.failClosed` gates queries on it alone. A syslog daemon outage or
  an unreachable bucket never blocks a query and never breaks the chain.
- **syslog** forwards each record as an RFC 5424 message (octet-counted
  framing on stream transports) — fire-and-forget with one reconnect
  attempt; failures count `sluice_audit_dropped_total{sink="syslog"}`.
  UDP is silently lossy by nature, and TCP syslog carries no TLS — put
  the collector on a trusted network. An unreachable daemon fails boot
  (visible misconfiguration).
- **s3** batches records into newline-delimited JSON objects
  (`<prefix>/YYYY/MM/DD/audit-<ulid>.jsonl`), uploading on a size or
  interval trigger. Failed uploads are retried from an in-memory buffer;
  past `maxBufferBytes` new records are dropped for this sink with
  `sluice_audit_dropped_total{sink="s3",reason="buffer_full"}` counting
  the loss. A process crash loses at most one un-uploaded batch — the
  file sink still has every record.
- **Object Lock**: the bucket must be *created* with Object Lock enabled
  (`aws s3api create-bucket --object-lock-enabled-for-bucket`); the sink
  sets per-object mode and retention but cannot enable locking on an
  existing bucket. With `credentialsRef` unset, credentials come from the
  standard env / shared-config / IAM chain (IRSA and instance profiles
  work with zero config).

## Retention without breaking the chain

- Prune **whole files only**, oldest first, in `(date, sequence)` order —
  never edit or truncate a file in place.
- Before pruning, run `audit verify` and archive the pruned files together
  with the genesis seed; the archived segment stays verifiable forever.
- Record the `hash` of the last pruned record: it equals the `prior_hash` of
  the first remaining record, so the live directory still verifies (without
  `--anchor`) and you can prove the two segments join.
- Ship copies offsite (ideally WORM storage) before local rotation removes them.
