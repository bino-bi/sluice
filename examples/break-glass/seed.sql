-- SPDX-License-Identifier: CC-BY-4.0
CREATE TABLE IF NOT EXISTS customers (
  id        INTEGER PRIMARY KEY,
  tenant_id TEXT    NOT NULL DEFAULT 'acme',
  email     TEXT    NOT NULL,
  created_at TEXT   NOT NULL
);

DELETE FROM customers;
INSERT INTO customers VALUES
  (1, 'acme', 'alice@acme.example', '2026-01-03'),
  (2, 'acme', 'bob@acme.example',   '2026-01-05'),
  (3, 'acme', 'carol@acme.example', '2026-01-09');
