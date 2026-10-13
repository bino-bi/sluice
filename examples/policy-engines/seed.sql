-- SPDX-License-Identifier: CC-BY-4.0

CREATE TABLE customers (
  id      INTEGER PRIMARY KEY,
  name    TEXT NOT NULL,
  country TEXT NOT NULL
);

CREATE TABLE payments (
  id           INTEGER PRIMARY KEY,
  customer_id  INTEGER NOT NULL,
  amount_cents INTEGER NOT NULL
);

INSERT INTO customers VALUES
  (1, 'Alice', 'DE'),
  (2, 'Bob',   'FR');

INSERT INTO payments VALUES
  (10, 1, 1999),
  (11, 2, 4599);
