-- SPDX-License-Identifier: CC-BY-4.0
-- pii-masking example — one customer table with four PII columns:
-- email, phone, ssn, birth_date. Column masks neutralise each in a
-- different way to illustrate the MVP providers (null + constant)
-- versus the v0.2 providers (partial + hash) that the policy declares
-- but currently downgrade to "null" until pkg/mask registers them.

CREATE TABLE IF NOT EXISTS customers (
  id         INTEGER PRIMARY KEY,
  tenant_id  TEXT    NOT NULL DEFAULT 'acme',
  email      TEXT    NOT NULL,
  phone      TEXT    NOT NULL,
  ssn        TEXT    NOT NULL,
  birth_date TEXT    NOT NULL,
  created_at TEXT    NOT NULL
);

DELETE FROM customers;

INSERT INTO customers VALUES
  (1, 'acme', 'alice@example.com',   '+1-555-0101', '111-22-3333', '1988-03-14', '2026-01-03'),
  (2, 'acme', 'bob@example.com',     '+1-555-0102', '222-33-4444', '1975-07-22', '2026-01-05'),
  (3, 'acme', 'carol@example.com',   '+1-555-0103', '333-44-5555', '1990-11-30', '2026-01-09'),
  (4, 'acme', 'dave@example.com',    '+1-555-0104', '444-55-6666', '2001-02-02', '2026-01-10');
