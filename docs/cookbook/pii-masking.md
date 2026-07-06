<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# PII masking

**Goal:** analysts query the `customers` table freely but never see plaintext
`email`, `phone`, or `ssn`; `birth_date` stays visible. Runnable version:
`examples/pii-masking/`.

## Ingredients

- One `SqlAccessPolicy` allowing the `analytics` group to read `customers`.
- Three `ColumnMaskPolicy` objects — one per PII column. Masks require an
  explicit `resources.columns` list; a mask without one matches nothing.

## The policies

The allow gate comes first — without it, default-deny blocks the query before
any mask applies. The example then neutralises each column with the two
simplest providers, `null` and `constant`:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata: { name: allow-analytics, priority: 100 }
spec:
  effect: allow
  match:
    any:
      - subjects:
          groups: ["analytics"]
        resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["customers"]
---
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata: { name: mask-email, priority: 90 }
spec:
  match:
    any:
      - resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["customers"]
          columns: ["email"]
  mask:
    type: "null"
---
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata: { name: mask-phone, priority: 80 }
spec:
  match:
    any:
      - resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["customers"]
          columns: ["phone"]
  mask:
    type: "constant"
    args:
      value: "+1-555-REDACTED"
---
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata: { name: mask-ssn, priority: 100 }
spec:
  match:
    any:
      - resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["customers"]
          columns: ["ssn"]
  mask:
    type: "constant"
    args:
      value: "REDACTED"
```

Priorities only matter when two masks collide on the same column — the
highest priority wins the column.

## Before and after

`SELECT id, email, phone, ssn, birth_date FROM shop.main.customers ORDER BY id`
returns:

| column | raw value | analyst sees |
|---|---|---|
| `email` | `alice@example.com` | `null` |
| `phone` | `+1-555-0101` | `+1-555-REDACTED` |
| `ssn` | `111-22-3333` | `REDACTED` |
| `birth_date` | `1988-03-14` | `1988-03-14` (no mask matches it) |

The rewritten SQL replaces the columns in the projection:

```sql
SELECT id, NULL AS email, '+1-555-REDACTED' AS phone, 'REDACTED' AS ssn, birth_date FROM shop.main.customers ORDER BY id
```

## Upgrade: structure-preserving providers

Swap the mask blocks above for providers that keep the data useful. `partial`
rewrites the SQL; `hash` with `hmac_sha256` and `fake` run **post-query**, on
result rows, after execution:

```yaml
# fragment — mask block for mask-email
mask:                       # keep the first 2 characters, star the rest
  type: partial
  args: { showFirst: 2, showLast: 0 }
```

```yaml
# fragment — mask block for mask-ssn
mask:                       # keyed HMAC hex: joinable, not reversible
  type: hash
  args:
    algorithm: hmac_sha256
    saltRef: "secret://env/SLUICE_MASK_SALT"
```

```yaml
# fragment — mask block for mask-phone
mask:                       # deterministic fake phone number
  type: fake
  args: { fakeType: phone }
```

`fake` supports `fakeType`: `name`, `first_name`, `last_name`, `email`,
`phone`, `company`, `city`, `country`, `uuid` — the same pattern covers a
`name` column when your schema has one.

## Test it without a server

Save as `policies.d/tests/masks.yaml` and run `sluice policy test policies.d`:

```yaml
cases:
  - name: email, phone and ssn are masked; birth_date is untouched
    identity: { subject: pii-analyst, groups: [analytics] }
    sql: "SELECT id, email, phone, ssn, birth_date FROM shop.main.customers"
    expect:
      outcome: allow
      masks:
        - "shop.main.customers.email=null"
        - "shop.main.customers.phone=constant"
        - "shop.main.customers.ssn=constant"
```

With the upgraded providers, `expect.masks` lists all winners
(`email=partial`, `phone=fake`, `ssn=hash`) and `expect.postMasks`
additionally asserts the two post-query ones:
`["shop.main.customers.phone=fake", "shop.main.customers.ssn=hash"]`.

## Pitfall: `sha256` vs `hmac_sha256`

`hash` with `algorithm: sha256` (the default) is rewritten into the SQL and is
**unkeyed**: anyone who can guess candidate values (SSNs, phone numbers) can
confirm them by hashing offline. For low-entropy PII always use
`algorithm: hmac_sha256`, which requires a `saltRef` secret and is applied
post-query. Manage the salt like a credential: store it via
`secret://env/...` or `secret://file/...`, never in the policy file, and plan
rotations — rotating the salt changes every masked output, which breaks joins
against previously exported data.

## See also

- [Column masks](../policies/column-masks.md) — the full provider catalog and argument reference.
- [Testing policies](../policies/testing.md) — `expect.masks` and `expect.postMasks`.
- [Break-glass access](break-glass.md) — letting one group bypass these masks, audited.
