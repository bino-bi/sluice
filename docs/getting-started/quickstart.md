<!-- SPDX-License-Identifier: CC-BY-4.0 -->

# Quickstart

`examples/hello-sluice/` is the smallest end-to-end Sluice deployment: a seeded SQLite catalog, an API-key identity, one allow policy, one row filter, one column mask, and a `curl` round-trip. This page walks through every file in it.

```
hello-sluice/
├── docker-compose.yaml          — builds the image, wires secrets and mounts
├── server.yaml                  — server configuration
├── seed.sql                     — schema + sample rows (3 tenants)
├── policies.d/
│   ├── datasource-shop.yaml     — DataSource (sqlite, /data/shop.db)
│   ├── binding-apikey.yaml      — SubjectBinding with one API key
│   ├── allow-analytics.yaml     — SqlAccessPolicy
│   ├── filter-tenant.yaml       — RowFilterPolicy
│   └── mask-email.yaml          — ColumnMaskPolicy (null)
└── data/                        — shop.db + audit files land here
```

## 1. Seed the catalog

```bash
cd examples/hello-sluice
sqlite3 data/shop.db < seed.sql
```

This creates `customers`, `orders`, and `admin_users` under the `main` schema, with rows for three tenants (`acme`, `widget`, `globex`).

## 2. The server configuration

`server.yaml`, abridged (comments and tuning knobs elided):

```yaml
rest:
  listen: ":8080"
admin:
  enabled: true
  listen: ":9091"
  token: "demo-admin-token-change-me"
policies:
  directory: "./policies.d"
  reload: true
audit:
  file:
    path: "/data/audit"
    rotateDaily: true
    rotateSizeMB: 64
    genesis: "secret://env/SLUICE_AUDIT_GENESIS"
identity:
  apiKeyPepper: "secret://env/SLUICE_APIKEY_PEPPER"
limits:
  maxRows: 10000
  queryTimeout: 15s
```

Everything declarative — the datasource, the identity binding, the policies — lives in the single `policies.d/` directory. `secret://env/...` references resolve from the environment; the compose file sets them.

## 3. Who is calling: the API-key binding

`policies.d/binding-apikey.yaml`:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: SubjectBinding
metadata:
  name: demo-apikeys
spec:
  claims:
    subjectId: "hello"
    tenantId: "acme"
    groups: "groups"
  apiKeys:
    - id: "sl_demo_hello"
      # hex(HMAC-SHA256(pepper, "sl_demo_hello:world"))
      hashRef: "secret://env/SLUICE_APIKEY_HELLO_HASH"
      groups: ["analytics"]
```

The demo key is `sl_demo_hello.world`. A presented key splits on its **last** `.` — everything left is the key id, everything right is the material. Sluice computes a constant-time HMAC-SHA256 over `id:material` with the server pepper and compares it to the secret behind `hashRef`. The stored hash comes from:

```bash
sluice apikey hash --pepper hello-sluice-demo-pepper --id sl_demo_hello --material world
# c0c9ab61795cbbb79767ef51dd0199cfbfd93ed4fd5fe767af1e81192f385153
```

The binding also stamps the subject (`hello`), tenant (`acme`), and API-key groups (`analytics`) onto the request, so policy templates like `{{ subject.tenantId }}` work without a JWT.

## 4. What they may do: the allow policy

`policies.d/allow-analytics.yaml`:

```yaml
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata:
  name: allow-analytics
  priority: 100
spec:
  effect: allow
  match:
    any:
      - subjects:
          groups: ["analytics"]
        resources:
          catalogs: ["shop"]
          schemas: ["main"]
          tables: ["customers", "orders"]
```

!!! note "Default-deny"
    Sluice never grants access implicitly. A query that no `SqlAccessPolicy` allows fails with `ACL_DENIED` and the message `no SqlAccessPolicy matched (default-deny)` — which is exactly what happens if you query the seeded `admin_users` table.

## 5. Start the server

```bash
docker compose up --build
```

Compose builds the image from the repository root, mounts `server.yaml`, `policies.d/`, and `data/`, and sets the three demo secrets (`SLUICE_APIKEY_PEPPER`, `SLUICE_AUDIT_GENESIS`, `SLUICE_APIKEY_HELLO_HASH`). To run `./bin/sluice serve --config server.yaml --policies-dir policies.d` directly instead, export those three variables yourself, and note that `server.yaml` and `datasource-shop.yaml` point at the container paths `/data/audit` and `/data/shop.db` — create `/data` or adjust both paths first.

## 6. The first query

```bash
curl -s \
  -H "X-Api-Key: sl_demo_hello.world" \
  -H "Content-Type: application/json" \
  -d '{"sql":"SELECT id, email, tenant_id FROM shop.main.customers ORDER BY id"}' \
  http://localhost:8080/v1/query
```

```json
{
  "query_id": "01KWV0EVV12RNE6HZRQRZGQRBA",
  "columns": ["id", "email", "tenant_id"],
  "rows": [[1, null, "acme"], [2, null, "acme"]],
  "row_count": 2,
  "truncated": false
}
```

Five customers are seeded, but only the two `acme` rows come back (the row filter), and `email` is `null` (the mask). [First query](first-query.md) dissects both.

## The debug loop

Whenever you touch a policy file, validate before you reload:

```bash
sluice policy validate examples/hello-sluice/policies.d
# policies OK: examples/hello-sluice/policies.d (5 objects, digest 5ec2ef1b5ff4)

sluice config validate examples/hello-sluice/policies.d --config examples/hello-sluice/server.yaml
# server config OK: examples/hello-sluice/server.yaml
# policies OK: examples/hello-sluice/policies.d (5 objects, digest 5ec2ef1b5ff4)
```

Both exit non-zero with a precise kind/name/field message on failure — the same pipeline the server runs on hot reload, so anything that validates here loads there.
