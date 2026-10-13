<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Testing policies

`sluice policy test` runs suites against a policy directory. A case has an identity, a SQL statement and the result you expect. It needs no server and no database.

## Example

The policy directory holds an allow, a row filter and a column mask.

```yaml
--8<-- "examples/policies/policies.d/allow.yaml"
```

The suite:

```yaml
--8<-- "examples/policies/tests/basic.yaml"
```

```bash
--8<-- "examples/policies/steps/01-policy-test.sh"
```

```text
--8<-- "examples/policies/expected/01-policy-test.txt"
```

The fourth case comes from a second suite, shown below.

## Suite format

A suite is a YAML file without `apiVersion`. The test runner ignores unknown keys: a typo such as `outcom` skips the check.

| Key | Meaning |
|---|---|
| `schema` | Columns of masked tables. See below. |
| `cases[].name` | Printed in the report. |
| `cases[].identity.subject` | Subject id. Without it, the case has no subject. |
| `cases[].identity.groups`, `.claims` | Groups and claims. Claims can nest. |
| `cases[].identity.issuer`, `.email` | Issuer and email. |
| `cases[].request.headers`, `.userAgent`, `.remoteIp` | Headers, user agent and address of the request, for the `request.*` values and for `ipRanges`. |
| `cases[].sql` | One SQL statement. |
| `cases[].expect` | The checks. Every field is optional. |

| `expect` field | Compared with |
|---|---|
| `outcome` | `allow`, `deny` or `reject`. |
| `denyPolicy` | Name of the deny policy. |
| `rejections` | Reject rules that fired, as `policy/rule`. |
| `filters` | Tables with a row filter, as `catalog.schema.table` in lower case. |
| `masks` | Every mask on the tables read, as `table.column=type`. The column is the policy's pattern, such as `phone_*`. |
| `postMasks` | The masks that run after the query, same form. |
| `applied` | Every policy in `Enforce` mode that matched, as `Kind/Name`. |
| `rewrittenSql` | The whole rewritten SQL. |
| `rewrittenSqlContains` | Parts of the rewritten SQL. |
| `errorCode` | The error that must happen: `ACL_DENIED` for a deny, the rule code for a reject, else the rewrite error. The test runner ignores the `errorCode` of a deny policy. |

Lists compare as sets, so the order is free. SQL checks ignore white space. An `allow` case fails when the rewrite fails, unless it sets `errorCode`.

```yaml
--8<-- "examples/policy-testing/tests/expect.yaml:deny"
```

## Schema fixtures for masked tables

The test runner has no database. For each masked table, list the columns under `schema`, in table order. The key is `catalog.schema.table`.

```yaml
--8<-- "examples/policies/tests/select-star.yaml"
```

Without the entry, the case fails and asks for a `schema` block. A table with only a row filter needs no entry, except under `SELECT *` in a query with a post-query mask.

## A failing case

The second case of this suite is wrong.

```yaml
--8<-- "examples/policy-testing/failing/wrong-outcome.yaml:2"
```

```bash
--8<-- "examples/policy-testing/steps/01-failing-case.sh"
```

```text
--8<-- "examples/policy-testing/expected/01-failing-case.txt"
```

With `--json`:

```bash
--8<-- "examples/policy-testing/steps/02-json.sh"
```

```json
--8<-- "examples/policy-testing/expected/02-json.txt"
```

## Flags and exit codes

| Flag | Meaning |
|---|---|
| `--tests <path>` | Suite file or directory, without subdirectories. Default: `<policy-dir>/tests`. |
| `--strict` | Refuse unknown fields in policies. |
| `--json` | Print the report as JSON. |

| Exit status | When |
|---|---|
| 0 | All cases pass. |
| 1 | Bad flag, missing tests path, broken suite file, or a policy compile error such as an unknown tag. |
| 3 | A policy with an invalid field, a failing case, or no case at all. |

## Limits

- Only the YAML engine runs. OPA and `RelationshipPolicy` do not.
- No SQL runs. A query on a missing column still passes.
- SQL that does not parse reads no table. A `deny` case with a typo can then pass, unless it sets `errorCode`.
- Not checked: timeouts, rate limits, budgets and the server row limit (`limits.maxRows`, `max_rows`). A case cannot expect a held query, but `applied` lists the `ApprovalPolicy`. A `QueryRewritePolicy` limit shows in `rewrittenSql`.
- No authentication method: `subjects.apiKeys` never matches and `subject.auth_method` is empty.

## See also

- [Policies](index.md)
- [CLI](../reference/cli.md)
- [Error codes](../reference/error-codes.md)
