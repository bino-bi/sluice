-- SPDX-License-Identifier: CC-BY-4.0

CREATE TABLE IF NOT EXISTS orders (
  id           INTEGER PRIMARY KEY,
  status       TEXT    NOT NULL,
  amount_cents INTEGER NOT NULL
);

DELETE FROM orders;

INSERT INTO orders (id, status, amount_cents) VALUES
  (1, 'paid',      1999),
  (2, 'open',      4599),
  (3, 'paid',      7800),
  (4, 'cancelled',  250),
  (5, 'open',     32000),
  (6, 'paid',      1200);
