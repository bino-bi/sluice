-- SPDX-License-Identifier: CC-BY-4.0

CREATE TABLE customers (
  id        INTEGER PRIMARY KEY,
  name      TEXT    NOT NULL,
  email     TEXT    NOT NULL,
  tenant_id TEXT    NOT NULL
);

INSERT INTO customers VALUES
  (1, 'Alice',   'alice@acme.test',   'acme'),
  (2, 'Bob',     'bob@acme.test',     'acme'),
  (3, 'Charlie', 'charlie@widget.io', 'widget');
