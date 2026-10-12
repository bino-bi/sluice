-- SPDX-License-Identifier: CC-BY-4.0

CREATE TABLE IF NOT EXISTS orders (
  id           INTEGER PRIMARY KEY,
  owner        TEXT    NOT NULL,
  region       TEXT    NOT NULL,
  status       TEXT    NOT NULL,
  amount_cents INTEGER NOT NULL
);

DELETE FROM orders;

-- --8<-- [start:rows]
INSERT INTO orders (id, owner, region, status, amount_cents) VALUES
  (1, 'ana',  'eu', 'paid',      1999),
  (2, 'ana',  'us', 'open',      4599),
  (3, 'ben',  'eu', 'paid',      7800),
  (4, 'ben',  'us', 'cancelled',  250),
  (5, 'cleo', 'eu', 'open',     32000);
-- --8<-- [end:rows]
