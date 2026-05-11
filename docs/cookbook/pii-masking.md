<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# PII masking

Goal: analysts can query `customers` freely, but never see the raw
email, phone number, or date of birth.

```yaml
---
apiVersion: sluice.bino-bi.github.io/v1
kind: SqlAccessPolicy
metadata: { name: customers-read }
spec:
  priority: 100
  effect:   allow
  selector: { groups: ['analytics'] }
  tables:   ['customers']
  statements: ['SELECT']
---
apiVersion: sluice.bino-bi.github.io/v1
kind: ColumnMaskPolicy
metadata: { name: email-null }
spec:
  priority: 90
  selector: { groups: ['analytics'] }
  table: 'customers'
  columns: ['email']
  mask:  { type: 'null' }
---
apiVersion: sluice.bino-bi.github.io/v1
kind: ColumnMaskPolicy
metadata: { name: phone-starred }
spec:
  priority: 90
  selector: { groups: ['analytics'] }
  table: 'customers'
  columns: ['phone']
  mask:
    type: 'constant'
    args: { value: '***-***-****' }
---
apiVersion: sluice.bino-bi.github.io/v1
kind: ColumnMaskPolicy
metadata: { name: dob-null }
spec:
  priority: 90
  selector: { groups: ['analytics'] }
  table: 'customers'
  columns: ['date_of_birth']
  mask:  { type: 'null' }
```

## Notes

- Higher-privilege groups (`data-eng`, `audit`) can override the mask
  with a higher `priority` ColumnMaskPolicy selecting those groups.
  Priority desc wins.
- Partial and hash providers land in v0.2 for number plates, card
  tails, and user IDs.
