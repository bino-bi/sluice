-- SPDX-License-Identifier: CC-BY-4.0

CREATE TABLE products (
  id          INTEGER PRIMARY KEY,
  name        TEXT    NOT NULL,
  price_cents INTEGER NOT NULL
);

INSERT INTO products VALUES
  (1, 'Ceramic Mug',  1299),
  (2, 'A5 Notebook',   799),
  (3, 'Black Pen',     199);

-- No policy allows this table.
CREATE TABLE customers (
  id    INTEGER PRIMARY KEY,
  email TEXT    NOT NULL
);

INSERT INTO customers VALUES
  (1, 'ada@example.com');
