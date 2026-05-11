<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Audit

Every terminal outcome — allow, deny, reject, error — emits exactly one
audit record to the configured sinks. In MVP `v0.1` the only sink is
the local JSONL file; S3 Object Lock, Postgres, syslog, and OTLP are on
the v1 roadmap.

## Record shape

Each line is a canonical JSON document with SHA-256 chaining:

```jsonc
{
  "query_id":   "01H...",
  "timestamp":  "2026-04-20T13:15:00.123456789Z",
  "subject":    { "id": "alice", "issuer": "https://auth/", "email": "…" },
  "request":    { "remote_ip": "10.…", "user_agent": "curl/…" },
  "sql_in":     "SELECT * FROM orders",
  "sql_out":    "SELECT * FROM (SELECT * FROM orders WHERE tenant_id = $1) …",
  "params":     ["acme"],
  "decision":   "allow",
  "applied":    [{"kind":"RowFilterPolicy","name":"tenant-isolation","…":"…"}],
  "row_count":  42,
  "truncated":  false,
  "prior_hash": "…",
  "hash":       "…"
}
```

## Hash chain

`hash = sha256(prior_hash || "\n" || canonical_body)`. The first record
(genesis) is anchored on `GenesisPriorHash(seed)` — a seed configured
in `server.yaml`. On startup, Sluice reads the tail of the most recent
file to pick up `last_hash` and continues the chain.

Tampering with any record — even a single whitespace change — breaks
every subsequent hash, and `sluice audit verify <dir>` fails at the
offending line.

## Rotation

Files are rotated daily (`audit-YYYY-MM-DD.jsonl`) and on a configurable
size cap (`audit-YYYY-MM-DD-N.jsonl`). Flush-then-fsync on every close;
open perms set to 0640.

## Backpressure

The dispatcher is backed by a bounded channel (default 10 000). Sinks
that fall behind mark records dropped via the
`sluice_audit_dropped_total` counter; callers never block waiting for a
disk sync. Operators should alert on non-zero drop counts.
