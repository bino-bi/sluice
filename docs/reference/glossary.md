<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Glossary

The docs use one term for each concept.

| Term | Meaning |
|---|---|
| [Admin API](admin-api.md) | Endpoints for the operator on a separate port. |
| [Allow](../policies/access-control.md) | A `SqlAccessPolicy` that lets a subject read a table. |
| [API key](../policies/subjects.md) | A secret that a client sends in the `X-Api-Key` header. |
| [Approval](../policies/approvals.md) | A hold on a query until a person accepts or rejects it. |
| [Audit log](../security/audit.md) | The files where Sluice records each query and its decision. |
| Audit record | One JSON line in the audit log. |
| [Budget](../policies/subjects.md) | A daily limit per subject on query time and rows. |
| Catalog | The name of a data source: the first part of `catalog.schema.table`. |
| [CEL](../policies/conditions.md) | Common Expression Language, used in conditions, reject rules and row filters. |
| Client | A program that sends queries to Sluice. |
| [Column mask](../policies/column-masks.md) | A policy that replaces the values of a column. |
| [Data source](../operations/data-sources.md) | A database or file store that Sluice reads. |
| Default deny | Sluice denies a query unless a policy allows every table it reads. |
| Deny | Sluice refuses a query because of access control: `ACL_DENIED`. |
| [Engine](../architecture/policy-engines.md) | The part of Sluice that decides on a query. |
| Genesis | The secret that starts the hash chain. |
| Grant | The right to run an approved query once. |
| Hash chain | Each audit record holds the hash of the record before it. |
| Mask type | How a column mask changes a value, for example `null` or `partial`. |
| [MCP](mcp.md) | Model Context Protocol. AI agents use it to call Sluice. |
| Pepper | A server secret that Sluice mixes into every API key hash. |
| [Policy](../policies/index.md) | A YAML object that tells Sluice what to allow, filter, mask, reject, rewrite or hold. |
| Policy directory | The directory with the YAML files that Sluice loads. |
| [Policy kind](policy-schema.md) | The `kind` of a policy, for example `RowFilterPolicy`. |
| [Reject](../policies/guardrails.md) | Sluice refuses a statement because of its form: `ACL_REJECTED`. |
| [Reload](../operations/hot-reload.md) | Sluice loads the policy directory again without a restart. |
| [Rewritten SQL](../architecture/request-lifecycle.md) | The statement that Sluice sends to DuckDB, with filters, masks and limits. |
| [Row filter](../policies/row-filters.md) | A policy that adds a condition to every read of a table. |
| [Secret reference](../operations/server-config.md) | A value such as `secret://env/NAME`. Sluice reads the secret from there. |
| [Selector](../policies/matching.md) | The subjects and tables a policy covers: `match` and `exclude`. |
| [Server config](configuration.md) | The YAML file that you pass to `sluice serve --config`. |
| Subject | The identity of a client, from its JWT or API key. |
| [Subject binding](../policies/subjects.md) | A `SubjectBinding` object. It maps credentials to subjects. |
| [Tag](../policies/classification.md) | A label on tables or columns, set by a `DataClassification` object. |
