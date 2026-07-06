<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Getting started

This track takes about ten minutes and ends with a running gateway that enforces real policies. You will stand up the `hello-sluice` example:

- a SQLite catalog (`shop`) seeded with customers and orders for three tenants,
- an API-key `SubjectBinding` that identifies the caller and stamps a tenant,
- one `SqlAccessPolicy` that grants access to exactly two tables,
- a `RowFilterPolicy` that scopes every query to the caller's tenant,
- a `ColumnMaskPolicy` that hides the `email` column,
- and one `curl` request that shows all of it applied on the wire.

Everything else — the default-deny posture, the rewritten SQL, the hash-chained audit trail — falls out of those five files.

## Prerequisites

One of:

- **Go 1.25 or later plus a C toolchain.** Sluice embeds pg_query and DuckDB, both cgo libraries, so `CGO_ENABLED=1` builds need a working C compiler.
- **Docker with Compose.** The example ships a `docker-compose.yaml` that builds the image from the repository root and wires up all secrets.

You will also want `sqlite3` (to seed the demo database) and `curl`.

## Chapters

1. [Install](install.md) — build the `sluice` binary from source, or skip straight to Docker.
2. [Quickstart](quickstart.md) — run `hello-sluice`: config, policies, API key, first request.
3. [First query](first-query.md) — dissect the request and response, tighten the mask, and use `sluice policy explain` to see why each policy fired.

When you are done, head to [Policies](../policies/index.md) to write your own.
