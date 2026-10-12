-- SPDX-License-Identifier: CC-BY-4.0

CREATE TABLE orders (
  id           INTEGER PRIMARY KEY,
  customer     TEXT    NOT NULL,
  amount_cents INTEGER NOT NULL
);

CREATE TABLE customers (
  id    INTEGER PRIMARY KEY,
  name  TEXT NOT NULL,
  email TEXT NOT NULL
);

CREATE TABLE salaries (
  employee     TEXT    NOT NULL,
  amount_cents INTEGER NOT NULL
);

INSERT INTO orders (id, customer, amount_cents) VALUES
  (1, 'Alice', 1999),
  (2, 'Bob',   4599),
  (3, 'Alice', 250);

INSERT INTO customers (id, name, email) VALUES
  (1, 'Alice', 'alice@example.com'),
  (2, 'Bob',   'bob@example.com');

INSERT INTO salaries (employee, amount_cents) VALUES
  ('Carol', 520000),
  ('Dan',   480000);
