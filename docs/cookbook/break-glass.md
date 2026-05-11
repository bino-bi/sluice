<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Break-glass access

Goal: a named group (`on-call`) can see raw PII during incident
response, but every query they run is called out in the audit log.

## Recipe

```yaml
---
apiVersion: sluice.bino-bi.github.io/v1
kind: SqlAccessPolicy
metadata: { name: oncall-read }
spec:
  priority: 110       # higher than the analyst policy so it wins when both match.
  effect:   allow
  selector: { groups: ['on-call'] }
  tables:   ['customers', 'orders']
  statements: ['SELECT']
---
apiVersion: sluice.bino-bi.github.io/v1
kind: ColumnMaskPolicy
metadata: { name: oncall-email-override }
spec:
  priority: 100       # same priority as the analyst mask wins by specificity.
  selector: { groups: ['on-call'] }
  table: 'customers'
  columns: ['email']
  mask:  { type: 'constant', args: { value: '{{ request.user_agent }}' } }
  # Use a harmless constant here in reality; the template is illustrative.
```

## Audit emphasis

Every query by an `on-call` caller lands in the audit log with the
applied policies list including `oncall-*`. A downstream log pipeline
can trip an alert the moment a break-glass query is emitted.

A future `QueryRejectPolicy` with `request.ticket_id` required-claim
check (v0.3) lets you demand an incident ticket reference for every
break-glass query.
