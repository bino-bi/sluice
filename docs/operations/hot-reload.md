<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Configuration reload

Sluice reloads the policy directory without a restart. Queries in flight
finish against the snapshot they started with; new queries see the new
snapshot atomically.

## Triggers

| Trigger | When to use |
| ------- | ----------- |
| fsnotify watcher | Default (`policies.reload: true`). GitOps deploys that write into the policy directory. |
| `SIGHUP` | `systemctl reload sluice`, or a one-off `kill -HUP` after copying files. |
| `POST /admin/reload` | Automation and operator tooling, via the admin listener. |

All three triggers require `policies.reload: true` (the default): with
`policies.reload: false`, the watcher is never built, `SIGHUP` becomes a
no-op, and `POST /admin/reload` returns `501` ("reload not enabled").

```bash
curl -X POST \
  -H "Authorization: Bearer $SLUICE_ADMIN_TOKEN" \
  http://localhost:9091/admin/reload
```

A successful call returns `{"ok": true, "digest": "..."}`; a failed one
returns an `ERR_CONFIG_INVALID` error and leaves the running snapshot
untouched.

The watcher covers the policy directory tree recursively, reacts only to
`.yaml`/`.yml` files (dotfiles and editor swap files are ignored), and
debounces bursts of writes into a single reload after 250 ms of quiet.
`SIGHUP` and the admin endpoint reload immediately, without debounce.

## Validate, then swap

Every reload re-reads the whole directory and validates it as a unit. Only
a fully valid set of manifests is published; any error rejects the reload
and the prior snapshot keeps serving. A bad file can therefore never take
policies down — but it also means your fix isn't live until the *entire*
directory validates again.

A successful swap updates, atomically:

- the compiled policy set (all kinds, including OPA modules, which are
  recompiled from `policies.opa.moduleDir`),
- API-key bindings — rebuilt, with the secret cache invalidated first so
  rotated `hashRef` values are re-read,
- JWT subject bindings — issuer set, audience, claim mappings, clock skew,
  and per-issuer HMAC secrets (the secret cache invalidation also re-reads
  rotated `hmacSecretRef` values; RS/ES keys refresh via each binding's
  JWKS cache TTL). A snapshot with duplicate issuers is rejected and the
  previous binding set stays live,
- rate-limit and budget specs from `SubjectBinding` manifests,
- the rewrite cache (purged — no decision outlives its snapshot),
- the schema cache (invalidated).

`ApprovalPolicy` objects hot-reload like every other policy kind. The
broker that serves them exists whenever `approval.publicBaseUrl` is set —
configure it even before the first ApprovalPolicy lands so a reload can
introduce one without a restart. Reload-added ApprovalPolicies without a
configured base URL log an error and matching queries pend until expiry.

## What does not hot-reload

`sluice.yaml` is read once at boot. Listener addresses (`rest.listen`,
`mcp.listen`, `admin.listen`), the policy engine selection
(`policies.engine`), limits (including the transport-level `globalRps` /
`perIpRps` buckets and `defaultSubjectRps`), `approval.publicBaseUrl`,
DuckDB pool settings, and audit configuration all require a restart. `DataSource` attachments are also built at boot —
the reload path does not re-attach catalogs, so restart after changing a
`DataSource` manifest.

## Safe rollout

1. **Validate and test in CI**: `sluice config validate ./policies.d
   --strict` plus [`sluice policy test`](../policies/testing.md).
2. **Deploy the files** into the policy directory.
3. **Reload** — or let the fsnotify watcher pick the change up.
4. **Spot-check** the live decision: `sluice policy explain --user <id>
   --table <catalog.schema.table>` locally, or
   `GET /admin/subjects/explain` against the running server, and confirm
   the `config watch: reload applied` log line.

## Observing reloads

Each applied reload logs at `INFO` (`config watch: reload applied`) with
the object count and snapshot digest, and `POST /admin/reload` returns the
new digest in its `{"ok": true, "digest": "..."}` response. There is
currently no dedicated Prometheus counter for reloads — watch the log line
(see [Observability](observability.md)).
