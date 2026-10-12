<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Break-glass access

Analysts read `email` masked. A member of the `sre` group reads the real value, and the audit log shows who did.

## Example

Start the server with `examples/break-glass/serve.sh`. The mask covers the `analytics` group. Its `exclude` takes the `sre` group out.

```yaml
--8<-- "examples/break-glass/policies.d/mask-email.yaml:2"
```

The SRE key is in both groups.

```yaml
--8<-- "examples/break-glass/policies.d/bindings.yaml:sre"
```

The analyst key reads `email`.

```bash
--8<-- "examples/break-glass/steps/01-analyst.sh"
```

```json
--8<-- "examples/break-glass/expected/01-analyst.txt"
```

The SRE key sends the same SQL.

```bash
--8<-- "examples/break-glass/steps/02-sre.sh"
```

```json
--8<-- "examples/break-glass/expected/02-sre.txt"
```

Find the SRE query in the audit log.

```bash
--8<-- "examples/break-glass/steps/03-audit-record.sh"
```

```json
--8<-- "examples/break-glass/expected/03-audit-record.txt"
```

**What happened:** The allow matches both keys through the `analytics` group. The `exclude` drops the mask for every subject in `sre`. The audit record names the subject and its groups.

## More examples

The `sre` group alone gives no access. This suite case proves it.

```yaml
--8<-- "examples/break-glass/tests/break-glass.yaml:sre-alone"
```

## Limits

- The audit record does not say that the mask was lifted. `policies_applied` still lists `mask-email`, and Sluice writes no separate record.
- The only sign is that `rewritten_fingerprint` equals `sql_fingerprint`: Sluice ran the SQL unchanged.
- To find such queries, search the `query` records for the group. The `query-result` records have an empty `subject`.
- The `exclude` is always on. For access that a person accepts each time, use an [approval](approval-workflow.md).

## See also

- [Matching](../policies/matching.md): how `exclude` works
- [Column masks](../policies/column-masks.md)
- [Audit log](../security/audit.md)
