<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Approval workflow

An `ApprovalPolicy` holds a query on sensitive columns until a person accepts it.

## Example

Start the server with `examples/approval-workflow/serve.sh`. It also starts `webhook.py`, which stands in for the approver's inbox.

The policy holds a query that names `ssn` or a `salary*` column, or that filters on `country = 'de'`.

```yaml
--8<-- "examples/approval-workflow/policies.d/approval.yaml:approval"
```

The server config needs the `approval` block. Sluice does not start without `publicBaseUrl`.

```yaml
--8<-- "examples/approval-workflow/server.yaml:approval"
```

Alice asks for `ssn`. Sluice holds the query.

```bash
--8<-- "examples/approval-workflow/steps/01-query-held.sh"
```

```json
--8<-- "examples/approval-workflow/expected/01-query-held.txt"
```

`webhook.py` stored the webhook that Sluice sent.

```bash
--8<-- "examples/approval-workflow/steps/02-webhook.sh"
```

```json
--8<-- "examples/approval-workflow/expected/02-webhook.txt"
```

The approver opens the accept link.

```bash
--8<-- "examples/approval-workflow/steps/03-accept.sh"
```

```json
--8<-- "examples/approval-workflow/expected/03-accept.txt"
```

Alice sends the same query again. Now it runs.

```bash
--8<-- "examples/approval-workflow/steps/04-query-runs.sh"
```

```json
--8<-- "examples/approval-workflow/expected/04-query-runs.txt"
```

She sends it a third time. Sluice holds it again.

```bash
--8<-- "examples/approval-workflow/steps/05-query-held-again.sh"
```

```json
--8<-- "examples/approval-workflow/expected/05-query-held-again.txt"
```

**What happened:** The accept created a grant for one run of the identical query. The second run used it up.

## More examples

The approver opens the reject link of the new approval.

```bash
--8<-- "examples/approval-workflow/steps/06-reject.sh"
```

```json
--8<-- "examples/approval-workflow/expected/06-reject.txt"
```

Alice reads the state of her approval with her own key.

```bash
--8<-- "examples/approval-workflow/steps/07-status.sh"
```

```json
--8<-- "examples/approval-workflow/expected/07-status.txt"
```

## Limits

- `columnsAccessed` sees only column names written in the query. `SELECT *` returns `ssn` without approval.
- The token in the link is the only check. Anyone who has the link can accept.

## See also

- [Approvals](../policies/approvals.md)
- [MCP tools](../reference/mcp.md): `check_approval` and `await_approval` for agents
- [Break-glass access](break-glass.md)
