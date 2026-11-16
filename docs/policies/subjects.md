<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Subjects, keys and budgets

A `SubjectBinding` maps an API key or a JWT to a subject. It can also set a rate limit and a daily budget. A request without a valid credential gets 401 `ERR_UNAUTHORIZED`.

## Example

Goal: a reporting job logs in with an API key.

```yaml
--8<-- "examples/subjects/policies.d/binding-apikey.yaml:2"
```

A key is `<id>.<secret>`. Sluice stores only its HMAC-SHA256 hash, keyed with the [pepper](../reference/glossary.md) in `identity.apiKeyPepper`. Without the pepper, Sluice accepts no API key.

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

| Field | Type | For a JWT | For an API key |
|---|---|---|---|
| `issuer` | string | Required `iss`. One binding per issuer. | Issuer of the subject. Default: binding name. |
| `audience` | string | Required `aud` entry. Required with `jwksUrl` or `hmacSecretRef`. | |
| `hmacSecretRef` | string | Secret for HS256, HS384, at least 32 bytes. Needs `issuer` and `audience`. | |
| `jwksUrl` | string | Keys for RS256, RS384, ES256, ES384. Needs `issuer` and `audience`. | |
| `jwksCacheTtl` | duration | Cache time of these keys. Default 10m. If a refresh fails, Sluice uses the old keys for at most 24 hours more and tries again every 30 seconds. After that, tokens of the issuer fail. | |
| `clockSkew` | duration | Tolerance for `exp`, `nbf` and `iat`. Default 60s. | |
| `claims.subjectId` | string | Path to the id. Default `sub`. | The id. Default: key id. |
| `claims.email` | string | Path to the email. Default `email`. | The email. |
| `claims.groups` | string | Path to a list or comma-separated string. Without it, the subject has no groups. | One group, for keys without `groups`. |
| `claims.tenantId`, `claims.allowedRegions` | string | Paths to the values for `{{ subject.tenantId }}`, `{{ subject.allowedRegions }}`. | The values themselves. |
| `apiKeys[]` | list | | `id`, `hashRef`, `groups` per key. |
| `rateLimit` | object | `rps`: requests per second. `burst`: requests at once, default 1. | Same. |
| `budget` | object | `rowsPerDay`, `cpuSecondsPerDay`: rows and wall-clock seconds per UTC day. 0: no limit. | Same. |

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

**What happened:** Sluice checked the signature, `iss`, `aud`, `exp`, `nbf` and `iat`. The list at `realm.roles` became the groups.

A token needs `exp`. Sluice accepts only the algorithms from the table, never `none`. A binding maps the subject id, the email, the groups, `tenantId` and `allowedRegions`. Templates and CEL read the other claims by their token names. A token with an `iat` in the future is refused.

### Rate limit and budget

Goal: after an agent read 3 rows on one day, Sluice refuses its next query.

```yaml
--8<-- "examples/subjects/policies.d/binding-agent.yaml:2"
```

Budgets are off until the server config turns them on.

```yaml
--8<-- "examples/subjects/server.yaml:budget"
```

The agent sends this query three times. The first run returns 3 rows.

```bash
--8<-- "examples/subjects/steps/04-agent.sh"
```

The second run:

```bash
--8<-- "examples/subjects/steps/05-agent-over-budget.sh"
```

```json
--8<-- "examples/subjects/expected/05-agent-over-budget.txt"
```

The third run:

```bash
--8<-- "examples/subjects/steps/06-agent-too-fast.sh"
```

```json
--8<-- "examples/subjects/expected/06-agent-too-fast.txt"
```

```bash
--8<-- "examples/subjects/steps/07-budget-show.sh"
```

```text
--8<-- "examples/subjects/expected/07-budget-show.txt"
```

**What happened:** Both refusals have status 429. Sluice checks the rate limit first, so a query over budget counts for it too.

## Limits

- A rate limit or budget needs `claims.subjectId` or `issuer` on the binding. With `issuer`, each subject has its own limit and counter.
- Each subject id has its own counter per issuer. Two issuers that issue the same id do not share a rate limit or budget.
- A limit for `claims.subjectId` applies to that id under the issuer of the binding: `issuer`, or the binding name for API keys. A binding with neither `issuer` nor `apiKeys` sets the limit of that id under every issuer.
- Sluice keeps the rate limit state of up to 10000 subjects. A subject whose state was dropped starts again with a full burst.
- Without `rateLimit`, `limits.defaultSubjectRps` and `limits.defaultSubjectBurst` apply.
- The rate limit and `limits.maxConcurrent` also count the MCP tools that list catalogs or tables, describe a table or explain access. The budget does not count them. The explain endpoint of the admin API counts only for `limits.maxConcurrent`.
- A reload resets all rate limits.
- Sluice counts usage after a query. One large query can pass the budget.
- A query that serves rows counts the DuckDB run time, without the wait for a DuckDB connection.
- A query that fails or times out counts the time since the request started, with 0 rows.
- The check sees only finished queries. Queries of one subject that run at the same time all pass it, so a subject can go over its budget by that much.
- `budget.db` in `budget.stateDir` holds one row per UTC day, subject id and issuer. At start, Sluice deletes rows older than `budget.retentionDays` (default 35).

## See also

- [Matching](matching.md)
- [Server config](../operations/server-config.md)
- [Configuration reference](../reference/configuration.md)
- [CLI](../reference/cli.md)
