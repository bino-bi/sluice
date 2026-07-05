# Policy testing

`sluice policy test <policy-dir>` runs declarative test suites against a
policy directory. Each case names an identity and a SQL statement and
asserts the resulting outcome, row filters, column masks, post-query
masks, applied policies, and rewritten SQL. The runner drives the same
`parse → policy.Evaluate → rewriter.Rewrite` pipeline the server uses, so
a passing suite reflects real enforcement behaviour.

## Layout

Suite files default to `<policy-dir>/tests/*.yaml` (override with
`--tests <file-or-dir>`). The `tests/` subdirectory is skipped by the
policy loader, so suites can live alongside the manifests they exercise.

```
policies.d/
  access.yaml
  filters.yaml
  tests/
    basic.yaml
```

## Suite format

```yaml
cases:
  - name: analyst reads orders with tenant filter
    identity:
      subject: alice
      groups: [analysts]
      claims: { tenant_id: acme }
    request:
      headers: { x-region: eu }     # optional per-request facts
    sql: "SELECT id FROM pg.public.orders"
    expect:
      outcome: allow                # allow | deny | reject
      filters: ["pg.public.orders"] # table keys with an active row filter
      masks: ["pg.public.customers.email=partial"]
      postMasks: ["pg.hr.employees.ssn=fpe"]
      applied:                      # "Kind/Name", order-independent
        - RowFilterPolicy/tenant-isolation
        - SqlAccessPolicy/allow-analysts
      rewrittenSqlContains: ["tenant_id = $1"]
      # rewrittenSql: "..."         # exact match (whitespace-normalised)
      # denyPolicy: "..."           # for deny outcomes
      # errorCode: ERR_MASK_UNSUPPORTED_CONTEXT
```

Every `expect` field is optional; only the fields you set are checked.
List assertions (`filters`, `masks`, `applied`, …) are compared as sets.

## Limitations

The runner uses no schema cache, so `SELECT *` combined with a column
mask cannot be resolved — write explicit column lists in test SQL.

## Exit codes

| Code | Meaning |
| ---- | ------- |
| 0 | All cases pass |
| 1 | I/O or flag error |
| 3 | Compile failure or one or more case failures |
