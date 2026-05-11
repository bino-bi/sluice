<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Column masks

A column mask substitutes a SQL expression at every reference site of a
protected column. In MVP v0.1 two providers ship:

| Provider   | Behaviour                                        |
| ---------- | ------------------------------------------------ |
| `null`     | Replace the column reference with `NULL`.        |
| `constant` | Replace with a typed literal, e.g. `'***'`.      |

Roadmap: `partial` (keep prefix/suffix), `hash` (SHA-256 with a salt).

## Where masks apply

The rewriter walks the full AST and replaces column references in:

- Projection list (`SELECT`)
- `WHERE` predicate
- `HAVING` predicate
- `GROUP BY`, `ORDER BY`
- `JoinExpr.Quals` — so `ON` conditions see the mask too.

Preserving column aliases means downstream tooling (Metabase,
Tableau, Python) receives the same column name it asked for.

## Tiebreakers

When multiple `ColumnMaskPolicy` objects match the same column on the
same table, the resolver picks in order:

1. `priority` desc.
2. Selector specificity desc (exact matches beat wildcards).
3. Policy name asc (lexicographic).

The deterministic ordering is verifiable via `sluice policy explain`.

## Example

```yaml
apiVersion: sluice.bino-bi.github.io/v1
kind: ColumnMaskPolicy
metadata:
  name: email-privacy
spec:
  priority: 80
  selector:
    groups: ['analytics']
  table: 'customers'
  columns: ['email']
  mask:
    type: null
```
