<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Subjects, keys and budgets

A `SubjectBinding` maps an API key or a JWT to a subject. It can also set a rate limit and a daily budget. A request without a valid credential gets 401 `ERR_UNAUTHORIZED`.

## Example

Goal: a reporting job logs in with an API key.

```yaml
--8<-- "examples/subjects/policies.d/binding-apikey.yaml:2"
```

A key is `<id>.<secret>`. Sluice stores only its HMAC-SHA256 hash, keyed with `identity.apiKeyPepper`. Without the pepper, Sluice accepts no API key.

```bash
--8<-- "examples/subjects/steps/01-hash.sh"
```

```text
--8<-- "examples/subjects/expected/01-hash.txt"
```

Store this value where `hashRef` points. The client sends the key.

```bash
--8<-- "examples/subjects/steps/02-apikey.sh"
```

```json
--8<-- "examples/subjects/expected/02-apikey.txt"
```

**What happened:** The key became the subject `reporting-bot` in group `analysts`. A policy allows that group. `Authorization: ApiKey <id>.<secret>` works too.

## Fields

| Field | For a JWT | For an API key |
|---|---|---|
| `issuer` | Required `iss`. One binding per issuer. | Issuer of the subject. Default: binding name. |
| `audience` | Required `aud` entry. Empty: no check. | |
| `hmacSecretRef` | Secret for HS256, HS384. | |
| `jwksUrl` | Keys for RS256, RS384, ES256, ES384. Needs `issuer` and `audience`. | |
| `jwksCacheTtl` | Cache time of these keys. Default 10m. | |
| `clockSkew` | Tolerance for `exp` and `nbf`. Default 60s. | |
| `claims.subjectId` | Path to the id. Default `sub`. | The id. Default: key id. |
| `claims.email` | Path to the email. Default `email`. | The email. |
| `claims.groups` | Path to a list or comma-separated string. | One group, for keys without `groups`. |
| `claims.tenantId`, `claims.allowedRegions` | | Values for `{{ subject.tenantId }}`, `{{ subject.allowedRegions }}`. |
| `apiKeys[]` | | `id`, `hashRef`, `groups` per key. |
| `rateLimit` | `rps`: tokens per second. `burst`: bucket size, default 1. | Same. |
| `budget` | `rowsPerDay`, `cpuSecondsPerDay`: rows and wall-clock seconds per UTC day. 0: no limit. | Same. |

A path looks like `$.realm.roles` or `tenants[0]`.

## More examples

### JWT

Goal: clients log in with a token from your identity provider.

```yaml
--8<-- "examples/subjects/policies.d/binding-jwt.yaml:2"
```

`mint-jwt.py` stands in for the identity provider. It signs these claims and adds `exp`.

```json
--8<-- "examples/subjects/token-claims.json"
```

```bash
--8<-- "examples/subjects/steps/03-jwt.sh"
```

```json
--8<-- "examples/subjects/expected/03-jwt.txt"
```

Sluice checks the signature, `iss`, `aud`, `exp` and `nbf`. A token needs `exp`. Sluice accepts only the algorithms from the table, never `none`.

A binding maps only the subject id, the email and the groups. Templates and CEL read the other claims by their token names.

### Rate limit and budget

Goal: an agent reads at most 3 rows per day.

```yaml
--8<-- "examples/subjects/policies.d/binding-agent.yaml:2"
```

Budgets are off until the server config turns them on.

```yaml
--8<-- "examples/subjects/server.yaml:budget"
```

```bash
--8<-- "examples/subjects/steps/04-agent.sh"
```

The agent sends this query three times. The first run returns 3 rows.

```json
--8<-- "examples/subjects/expected/05-agent-over-budget.txt"
```

```json
--8<-- "examples/subjects/expected/06-agent-too-fast.txt"
```

Both answers have status 429. Sluice checks the rate limit first, and each request takes a token.

```bash
--8<-- "examples/subjects/steps/07-budget-show.sh"
```

```text
--8<-- "examples/subjects/expected/07-budget-show.txt"
```

## Limits

- A rate limit or budget needs `claims.subjectId` or `issuer` on the binding. With `issuer`, each subject has its own bucket and counter.
- Without `rateLimit`, `limits.defaultSubjectRps` and `limits.defaultSubjectBurst` apply.
- A reload resets all rate limit buckets.
- Sluice counts usage after a query. One large query can pass the budget.
- `budget.db` in `budget.stateDir` holds one row per UTC day, subject id and issuer. At start, Sluice deletes rows older than `budget.retentionDays` (default 35).

## See also

- [Matching](matching.md)
- [Server config](../operations/server-config.md)
- [Configuration reference](../reference/configuration.md)
- [CLI](../reference/cli.md)
