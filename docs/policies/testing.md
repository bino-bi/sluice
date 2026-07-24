<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Testing policies

`sluice policy test <policy-dir>` runs declarative test suites against a policy directory: each
case names an identity and a SQL statement and asserts the resulting decision. The runner drives
the same parse → evaluate → rewrite pipeline the server uses, so a passing suite reflects real
enforcement behaviour — treat it as the unit test suite for your authorization rules.

## Layout

Suite files default to `<policy-dir>/tests/*.yaml` (override with `--tests <file-or-dir>`). The
`tests/` subdirectory is one of the directories the policy loader skips (along with `testdata`
and dot-directories), so suites can live right next to the manifests they exercise:

```
policies.d/
  access.yaml
  filters.yaml
  tests/
    basic.yaml
```

## Suite format

A suite is a plain YAML file with a `cases` list — it is not a policy object and carries no
`apiVersion`. Every `expect` field is optional; only the fields you set are checked:

```yaml
cases:
  - name: analyst reads orders with tenant filter
    identity:
      subject: alice                   # subject id; omit to simulate anonymous
      issuer: https://idp.example      # optional
      email: alice@example.com         # optional
      groups: [analysts]               # optional
      claims: { tenant_id: acme }      # optional, nested maps allowed
    request:                           # optional per-request facts
      headers: { x-region: eu }
      userAgent: "bi-tool/2.1"
    sql: "SELECT id FROM pg.public.orders"
    expect:
      outcome: allow                   # allow | deny | reject
      filters: ["pg.public.orders"]    # table keys carrying a row filter
      masks: ["pg.public.customers.email=partial"]  # "table.column=type"
      postMasks: ["pg.hr.employees.ssn=fpe"]        # post-query masks, same format
      applied:                         # "Kind/Name" of every enforcing policy
        - SqlAccessPolicy/allow-analysts
        - RowFilterPolicy/tenant-isolation
      rewrittenSqlContains: ["tenant_id = $1"]      # substrings of the final SQL
      # rewrittenSql: "SELECT ..."                  # exact match instead
      # denyPolicy: deny-salaries                   # deny outcomes: which policy
      # errorCode: ERR_MASK_UNSUPPORTED_CONTEXT     # expected rewrite-stage error
      # rejections: ["query-guardrails/no-select-star-with-joins"]  # "policy/rule"
```

How assertions compare:

- List assertions (`filters`, `masks`, `postMasks`, `applied`, `rejections`) are compared as
  **sorted sets** — order never matters, but the sets must be equal, not merely overlapping.
- `rewrittenSql` and `rewrittenSqlContains` are **whitespace-normalised** on both sides.
- `errorCode` asserts the error a failing rewrite raises — see the
  [error code reference](../reference/error-codes.md).

### Schema fixtures for `SELECT *`

The test runner has no live datasource, so by itself it cannot expand `SELECT *` against a
schema — a star case combined with a column mask would fail with a schema-missing rewrite error.
A suite that needs star expansion declares its tables in an optional top-level `schema:` block:

```yaml
schema:
  pg.public.customers: [id, email, tenant_id]   # catalog.schema.table: column list

cases:
  - name: star expands and masks email
    identity: { subject: alice, groups: [analysts] }
    sql: "SELECT * FROM pg.public.customers"
    expect:
      outcome: allow
      masks: ["pg.public.customers.email=partial"]
      rewrittenSqlContains: ["substr(customers.email"]
```

Keys must be fully-qualified three-part `catalog.schema.table` names, matching the three-part
table names in the test SQL. The block is static fixture data, never introspection — keep it in
sync with the real table by hand. Suites without a `schema:` block behave exactly as before.

## Running suites

Each failing case prints its assertion diff (`outcome: want "deny" got "allow"`). Flags:
`--tests <file-or-dir>` picks the suites, `--strict` rejects unknown YAML fields in the policies,
`--json` emits the report as JSON.

| Exit code | Meaning |
| --------- | ------- |
| 0 | All cases pass |
| 1 | I/O or flag error |
| 3 | Policy compile failure, any case failure — or **zero cases ran** |

Zero cases exiting 3 is deliberate: a suite directory that silently matches no files fails CI
instead of passing it.

## A worked example

The repository ships a runnable example in `examples/policies/`: an allow for the `analysts`
group, a tenant row filter on `orders`, and a partial mask on `customers.email`. Its main suite,
`examples/policies/tests/basic.yaml` (a second suite, `select-star.yaml`, demonstrates the
`schema:` fixture block):

```yaml
cases:
  - name: analyst reads orders with tenant filter
    identity: { subject: alice, groups: [analysts], claims: { tenant_id: acme } }
    sql: "SELECT id FROM pg.public.orders"
    expect:
      outcome: allow
      filters: ["pg.public.orders"]
      applied: ["RowFilterPolicy/tenant-isolation", "SqlAccessPolicy/allow-analysts"]
      rewrittenSqlContains: ["tenant_id = $1"]

  - name: non-analyst is denied by default
    identity: { subject: mallory, groups: [guests] }
    sql: "SELECT id FROM pg.public.orders"
    expect:
      outcome: deny

  - name: email is partially masked
    identity: { subject: alice, groups: [analysts] }
    sql: "SELECT id, email FROM pg.public.customers"
    expect:
      outcome: allow
      masks: ["pg.public.customers.email=partial"]
      rewrittenSqlContains: ["substr(email"]
```

```console
$ sluice policy test examples/policies
PASS  analyst reads orders with tenant filter
PASS  non-analyst is denied by default
PASS  email is partially masked
PASS  star expands and masks email via schema fixture

4 passed, 0 failed, 4 total
```

Note the second case: default-deny needs no deny policy, only the absence of a matching allow.

## Continuous integration

Validate and test on every change to the policy directory. Sluice is built from source (CGO —
pg_query and DuckDB — so the runner needs a C toolchain, which `ubuntu-latest` has):

```yaml
# fragment — GitHub Actions job steps
- uses: actions/checkout@v4
- uses: actions/setup-go@v5
  with:
    go-version: "1.25"
- name: Build sluice
  run: make build
- name: Validate policies
  run: ./bin/sluice policy validate ./policies.d --strict
- name: Test policies
  run: ./bin/sluice policy test ./policies.d
```

## Debugging a failing case

When a case fails and the assertion diff is not enough, replay the identity against the same
directory with `sluice policy explain`:

```console
$ sluice policy explain --policies-dir examples/policies \
    --user alice --groups analysts --claims tenant_id=acme --table pg.public.orders
subject : alice
resource: pg.public.orders
decision: allow
Kind             Name              Priority  Effect
----             ----              --------  ------
RowFilterPolicy  tenant-isolation  50        applied
SqlAccessPolicy  allow-analysts    0         applied
row filters:     pg.public.orders
```

It lists every enforcing policy that matched and the effective decision, which usually pinpoints
whether the selector, the priority, or the identity is wrong. Add `--json` to also see
`Audit`/`DryRun` shadow matches (reported in the `shadow` field).
