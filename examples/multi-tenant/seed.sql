-- SPDX-License-Identifier: CC-BY-4.0
-- Multi-tenant example — three tenants share one catalog. The RowFilterPolicy
-- rewrites every query to constrain `tenant_id` to the caller's tenant,
-- proven by two API keys seeing disjoint result sets over the same SQL.

CREATE TABLE IF NOT EXISTS customers (
  id         INTEGER PRIMARY KEY,
  tenant_id  TEXT    NOT NULL,
  email      TEXT    NOT NULL,
  created_at TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS orders (
  id         INTEGER PRIMARY KEY,
  tenant_id  TEXT    NOT NULL,
  customer_id INTEGER NOT NULL,
  amount_cents INTEGER NOT NULL,
  created_at TEXT    NOT NULL
);

DELETE FROM customers;
DELETE FROM orders;

INSERT INTO customers (id, tenant_id, email, created_at) VALUES
  (1, 'acme',   'a1@acme.example',  '2026-01-10'),
  (2, 'acme',   'a2@acme.example',  '2026-01-12'),
  (3, 'beta',   'b1@beta.example',  '2026-02-04'),
  (4, 'beta',   'b2@beta.example',  '2026-02-05'),
  (5, 'gamma',  'g1@gamma.example', '2026-03-01');

INSERT INTO orders (id, tenant_id, customer_id, amount_cents, created_at) VALUES
  (10, 'acme',  1, 1999,  '2026-01-15'),
  (11, 'acme',  2, 4599,  '2026-01-18'),
  (12, 'beta',  3, 7800,  '2026-02-10'),
  (13, 'beta',  4, 250,   '2026-02-11'),
  (14, 'gamma', 5, 32000, '2026-03-05');
