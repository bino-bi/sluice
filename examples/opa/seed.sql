-- SPDX-License-Identifier: CC-BY-4.0

CREATE TABLE orders (
  id        INTEGER PRIMARY KEY,
  tenant_id TEXT    NOT NULL,
  amount    INTEGER NOT NULL
);

INSERT INTO orders (id, tenant_id, amount) VALUES
  (1, 'acme', 120),
  (2, 'acme', 80),
  (3, 'beta', 45);
