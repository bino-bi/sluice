<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# First query

With `hello-sluice` running, send a query that a real user would send:

```bash
curl -sS -X POST http://localhost:8080/v1/query \
  -H 'Authorization: ApiKey hello.world' \
  -H 'Content-Type: application/json' \
  -d '{"sql":"SELECT id, customer_email, amount FROM orders"}'
```

The response is streamed JSON:

```json
{
  "query_id": "01H…",
  "columns": ["id", "customer_email", "amount"],
  "rows": [
    [1, null, 12.50],
    [2, null, 22.00]
  ],
  "row_count": 2,
  "truncated": false
}
```

Three things happened on the server side:

1. **Row filter** collapsed the three-tenant dataset down to two
   `acme`-only rows via the templated predicate
   `tenant_id = '{{ subject.tenantId }}'` (rendered through a positional
   parameter, never concatenation).
2. **Column mask** nulled the `customer_email` column before the result
   left DuckDB.
3. **Audit** appended one hash-chained record describing who asked,
   what SQL arrived, what SQL actually ran, the row count, and the
   verdict.

Inspect the audit file:

```bash
tail -n 1 examples/hello-sluice/data/audit/audit-*.jsonl | jq .
./bin/sluice audit verify examples/hello-sluice/data/audit
# chain OK (1 file(s), 2 record(s), last_hash=…)
```

## Where next

- Craft your own policies — [Concepts → Policies](../concepts/policies.md).
- Wire a real Postgres catalog —
  [Operations → Data sources](../operations/data-sources.md).
- Hook up an LLM agent — [Reference → MCP tools](../reference/mcp.md).
