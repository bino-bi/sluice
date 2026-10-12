<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Key rotation

This page shows how to change each secret that Sluice uses.

## Example

Goal: give a client a new API key while the old key still works.

Store the hash of the new key in a file that only the owner can write:

```bash
--8<-- "examples/key-rotation/steps/02-hash-new-key.sh"
--8<-- "examples/key-rotation/steps/03-protect-hash-file.sh"
```

Add a second entry to the subject binding. The example keeps its hash files in `data/secrets`:

```yaml
--8<-- "examples/key-rotation/both/binding-report-bot.yaml:2"
```

Reload:

```bash
--8<-- "examples/key-rotation/steps/05-reload.sh"
```

```json
--8<-- "examples/key-rotation/expected/05-reload.txt"
```

Both keys work now. The new key:

```bash
--8<-- "examples/key-rotation/steps/07-new-key.sh"
```

```json
--8<-- "examples/key-rotation/expected/07-new-key.txt"
```

When the client uses the new key, remove the old entry and reload. Then the old key gets this answer:

```bash
--8<-- "examples/key-rotation/steps/10-old-key-after.sh"
```

```json
--8<-- "examples/key-rotation/expected/10-old-key-after.txt"
```

What happened: Sluice reads every `hashRef` again on a reload. The new key still works.

## Secrets

| Secret | Key | New value works | What to know |
|---|---|---|---|
| API key | `apiKeys[].hashRef` | After a reload (`secret://file/`) | Use two entries with two ids, as in the example |
| [Pepper](../reference/glossary.md) | `identity.apiKeyPepper` | After a restart | Every key hash changes. First compute all hashes again with `sluice apikey hash` |
| JWT key (RS, ES) | [JWKS](../reference/glossary.md) of the issuer | On first use | Publish the new key next to the old one. A removed key works until `jwksCacheTtl` ends |
| JWT secret (HS) | `hmacSecretRef` | After a reload (`secret://file/`) | One secret per issuer. Old tokens fail at once |
| Audit [genesis](../reference/glossary.md) | `audit.file.genesis` | Only in an empty audit directory | Set a new `audit.file.path` too, then restart. Verify each directory with its own [`--anchor`](../reference/glossary.md) |
| Admin token | `admin.token` | After a restart | `/metrics` needs the same token |
| Data source credentials | `credentialsRef`, `tokenRef`, `headersRef` | After a restart | Keep the old credential valid at the source until then |
| [Column mask](../policies/column-masks.md) salt and `fpe` key | `saltRef`, `keyRef` | After a reload (`secret://file/`) | The same input gives a new masked value |

Sluice reads a `secret://env/` value from its own process environment. A new or changed variable needs a restart.

## Limits

- Sluice uses the pepper and the genesis byte for byte. A newline at the end of a secret file is part of the value.
- Without a reload, Sluice can use an old mask salt for up to 10 minutes.
- With `claims.subjectId`, the audit log does not show which key a client used.

## See also

- [Subjects](../policies/subjects.md): API keys and JWT.
- [Reload](../operations/hot-reload.md)
- [Server config](../operations/server-config.md): `secret://` references.
