<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Key rotation

This page shows how to change each secret that Sluice uses.

## Example

Goal: give a client a new API key while the old key still works.

Compute the hash of the new key and store it as a secret:

```bash
--8<-- "examples/key-rotation/steps/01-hash-new-key.sh"
```

```text
--8<-- "examples/key-rotation/expected/01-hash-new-key.txt"
```

Add a second entry to the subject binding and reload:

```yaml
--8<-- "examples/key-rotation/policies.d/binding-report-bot.yaml:2"
```

Both keys work now. When the client uses the new key, remove the old entry and reload. The example copies a binding without the old entry:

```bash
--8<-- "examples/key-rotation/steps/04-remove-old-key.sh"
--8<-- "examples/key-rotation/steps/05-reload.sh"
```

Then the old key gets this answer:

```bash
--8<-- "examples/key-rotation/steps/06-old-key-after.sh"
```

```json
--8<-- "examples/key-rotation/expected/06-old-key-after.txt"
```

What happened: Sluice reads every `hashRef` again on a reload. The new key still works.

## Secrets

| Secret | Key | New value works | What to know |
|---|---|---|---|
| API key | `apiKeys[].hashRef` | After a reload | Use two entries with two ids, as in the example |
| Pepper | `identity.apiKeyPepper` | After a restart | Every key hash changes. First compute all hashes again with `sluice apikey hash` |
| JWT key (RS, ES) | JWKS of the issuer | On first use | Publish the new key next to the old one. A removed key works until `jwksCacheTtl` ends |
| JWT secret (HS) | `hmacSecretRef` | After a reload | One secret per issuer. Old tokens fail at once |
| Audit genesis | `audit.file.genesis` | Only in an empty audit directory | Set a new `audit.file.path` too, then restart. Verify each directory with its own `--anchor` |
| Admin token | `admin.token` | After a restart | `/metrics` needs the same token |
| Data source credentials | `credentialsRef`, `tokenRef`, `headersRef` | After a restart | Keep the old credential valid at the source until then |
| Mask salt, FPE key | `saltRef`, `keyRef` | After a reload | The same input gives a new masked value |

## Limits

- Sluice uses the pepper and the genesis byte for byte. A newline at the end of a secret file is part of the value.
- Without a reload, Sluice can use an old mask salt for up to 10 minutes.
- With `claims.subjectId`, the audit log does not show which key a client used.

## See also

- [Subjects](../policies/subjects.md): API keys and JWT.
- [Reload](../operations/hot-reload.md)
- [Server config](../operations/server-config.md): `secret://` references.
