<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Quickstart

Run the `hello-sluice` example and send your first query through Sluice.

These docs describe the `main` branch, so you build Sluice from source. You need Git, `make`, Go 1.26 or later, a C compiler, `python3` and `curl`.

## 1. Build and start

```bash
--8<-- "examples/install/manual/build.sh"
```

Start the example:

```bash
--8<-- "examples/install/manual/start-example.sh"
```

The script creates the SQLite database `data/shop.db` with the tables `customers`, `orders` and `admin_users` for three tenants. Then it sets demo secrets and starts Sluice. On the first start, Sluice downloads the DuckDB extension for SQLite. Sluice listens on `http://localhost:8080`. The admin API listens on port 9091.

Leave the server running. Open a second terminal in the `sluice` folder:

```bash
--8<-- "examples/install/manual/second-terminal.sh"
```

## 2. Send a query

```bash
--8<-- "examples/hello-sluice/steps/01-query.sh"
```

```json
--8<-- "examples/hello-sluice/expected/01-query.txt"
```

The outputs are real, but indented. Values that change between runs appear as placeholders such as `<query-id>`.

The API key maps to the [subject](../reference/glossary.md) `hello` with the group `analytics` and the tenant `acme`. The policy `allow-analytics` lets this group read `customers` and `orders`.

The row filter kept only the rows of tenant `acme`, two of five customers.

```yaml
--8<-- "examples/hello-sluice/policies.d/filter-tenant.yaml:2"
```

The column mask replaced every `email` with null.

```yaml
--8<-- "examples/hello-sluice/policies.d/mask-email.yaml:2"
```

## 3. Read a table that no policy allows

```bash
--8<-- "examples/hello-sluice/steps/02-denied.sh"
```

```json
--8<-- "examples/hello-sluice/expected/02-denied.txt"
```

Sluice denies by default. No policy allows `admin_users`, so the client gets HTTP status 403.

## 4. Ask which policies apply

`sluice policy explain` takes a subject and a table. It reads the policy directory and needs no running server.

```bash
--8<-- "examples/hello-sluice/steps/04-policy-explain.sh"
```

```text
--8<-- "examples/hello-sluice/expected/04-policy-explain.txt"
```

## 5. Verify the audit log

Sluice wrote an audit record for each decision. Each record holds the hash of the record before it.

```bash
--8<-- "examples/hello-sluice/steps/03-audit-verify.sh"
```

```text
--8<-- "examples/hello-sluice/expected/03-audit-verify.txt"
```

`chain OK` means that the hash chain is intact. The four records are the start of the chain, two for the first query and one for the denied query.

Stop the server with `Ctrl+C` in the first terminal.

## Run it in Docker

The compose file of the example pulls a released image. A release can behave differently from these docs, so build the image from your checkout. You need Docker with Compose, `python3` and `curl`. Run this in the repository root:

```bash
--8<-- "examples/install/manual/docker-example.sh:up"
```

The first build compiles DuckDB and takes about 30 minutes. From a second terminal, send the `curl` commands of sections 2 and 3. Run the `sluice` command of section 5 inside the container, from `examples/hello-sluice`:

```bash
--8<-- "examples/install/manual/docker-example.sh:cli"
```

`make docs-check` does not run these commands.

## See also

- [Policies](../policies/index.md): how policies work and how to write your own.
- [Cookbook](../cookbook/index.md): complete setups for common cases.
- [REST API](../reference/rest-api.md): all request and response fields.
- [Install](install.md): release archives and container images.
