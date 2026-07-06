<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Key rotation

Everything long-lived in a Sluice deployment can be rotated. The table shows
what, where it is configured, when a new value takes effect, and what breaks.

| Material | Configured at | New value takes effect | Rotation invalidates |
|---|---|---|---|
| API-key pepper | `identity.apiKeyPepper` (`secret://`) | Restart | **Every** API-key hash |
| Individual API key | `SubjectBinding.spec.apiKeys[].hashRef` | Policy reload | That key only |
| JWT signing keys (RS/ES) | Issuer's JWKS endpoint | JWKS cache refresh | Nothing, if overlapped |
| HS JWT secret | `SubjectBinding.spec.hmacSecretRef` | Restart | All HS tokens for that issuer |
| Audit genesis seed | `audit.file.genesis` | Restart | Nothing — starts a new chain |
| Mask salt / FPE key | `saltRef` / `keyRef` in `ColumnMaskPolicy` | Reload (new ref) or ≤10 min cache (in place) | All masked/tokenized outputs |
| Admin bearer token | `admin.token` (or `SLUICE_ADMIN__TOKEN`) | Restart | Operator tooling only |

## API-key pepper

Stored hashes are `HMAC-SHA256(pepper, id + ":" + material)`, so a new pepper
invalidates every hash at once. The pepper is resolved at startup and is not
hot-reloaded.

1. Generate and store the new pepper (`openssl rand -hex 32`).
2. Recompute every key's hash with the new pepper — this needs the original
   key material: `sluice apikey hash --pepper <new-ref> --id report-bot
   --material <material>`. For keys whose material you never retained, issue
   replacement keys instead.
3. Update every `hashRef` secret with the recomputed values.
4. Switch `identity.apiKeyPepper` and restart.

A single instance accepts exactly one pepper, so steps 3–4 are a hard
cutover. For zero downtime, run a second instance with the new pepper behind
your load balancer and drain the old one.

## Individual API keys

`hashRef` values are re-resolved on every policy reload (the secret cache is
invalidated first), so per-key rotation is hot. Overlap with dual entries:

```yaml
# fragment — dual key entries during the overlap window
apiKeys:
  - id: report-bot-2026a
    hashRef: secret://env/REPORT_BOT_2026A_HASH
    groups: ["agents"]
  - id: report-bot-2026b
    hashRef: secret://env/REPORT_BOT_2026B_HASH
    groups: ["agents"]
```

1. Add the new entry alongside the old and reload
   (see [Configuration reload](../operations/hot-reload.md)).
2. Distribute the new key (`<id>.<material>`) to the client.
3. Once traffic has moved (check `subject.id` in the audit log — it carries
   the key id when the binding does not set `claims.subjectId`), remove the
   old entry and reload again.

## JWT signing keys (RS/ES via JWKS)

Rotation happens on the issuer's side; Sluice follows automatically:

1. Publish the new key alongside the old one in the issuer's JWKS.
2. Start signing tokens with the new `kid`. Sluice caches JWKS per URL for
   `jwksCacheTtl` (default 10 minutes), and an unknown `kid` triggers an
   immediate refresh, rate-limited to one per 30 seconds — new keys are
   picked up on first use.
3. Remove the old key from the JWKS after its tokens have expired.

Lower `jwksCacheTtl` on the `SubjectBinding` if you need faster convergence
on key *revocation* (removal only propagates when the cache expires).

## HS JWT secrets (`hmacSecretRef`)

HS secrets are resolved once at startup, and there is one secret per issuer —
rotation is a hard cutover: update the secret, restart, and every token
signed with the old secret fails immediately. Coordinate with token issuance,
or prefer RS/ES with JWKS where graceful rotation matters.

## Audit genesis seed

Rotating the seed starts a new hash chain; it does not touch existing files.

1. Verify the current chain and archive the audit directory together with a
   note of the old seed.
2. Stop the instance; point `audit.file.path` at a fresh directory (a mixed
   directory will not verify — the new genesis does not chain from the old
   tail).
3. Generate the new seed, update `audit.file.genesis`, and start.
4. Verify each segment against its own anchor (`sha256` of the seed in hex):

```console
$ sluice audit verify /srv/audit-archive/2026-h1 --anchor "$OLD_ANCHOR"
$ sluice audit verify /var/lib/sluice/audit --anchor "$NEW_ANCHOR"
```

## Mask salts and FPE keys

`hash` masks with `algorithm: hmac_sha256` take a `saltRef`, and `fpe` masks
take a `keyRef`. Both are deterministic: the same input yields the same
output *only while the secret is unchanged*.

!!! warning "Rotation changes every masked output"
    After rotating a salt or FPE key, the same email or account number masks
    to a **different** value. Joins on masked columns across queries or
    exports stop lining up, and anything downstream keyed on the old outputs
    goes stale. Rotate deliberately and tell downstream consumers.

No restart is needed either way. Point the mask at a **new** secret reference
and reload the policies — the new URI resolves immediately. If you instead
overwrite the secret's value in place (same URI), the old value can be served
from the secret cache for up to 10 minutes before the next masked query picks
up the rotated one.

## Admin bearer token

A static string compared in constant time. Set it via the
`SLUICE_ADMIN__TOKEN` environment variable and restart to rotate. Update your
Prometheus scrape credentials and operator tooling at the same time — the
`/metrics` endpoint sits behind the same token.

## What rotation does not invalidate

Audit chain segments already on disk stay verifiable forever: each record's
hash was computed when it was written, and each segment verifies against the
genesis seed it started from. Rotating peppers, keys, JWKS material, or mask
secrets never touches the audit chain.
