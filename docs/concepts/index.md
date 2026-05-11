<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Concepts

Five ideas are enough to understand how Sluice behaves.

- [**Policies**](policies.md) — the declarative unit that grants
  access, rejects queries, or applies row/column transformations.
- [**Row filters**](row-filters.md) — a WHERE-clause predicate injected
  into the query as a subquery wrapper.
- [**Column masks**](column-masks.md) — a substitution at every
  reference site of a protected column (null, constant, partial, hash).
- [**Identity**](identity.md) — who the caller is and how Sluice learns
  about them (API keys, JWT).
- [**Audit**](audit.md) — the tamper-evident record of what ran.

Each page is intentionally short; deep references are in the
[Reference](../reference/index.md) section.
