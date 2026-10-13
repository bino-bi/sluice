-- SPDX-License-Identifier: CC-BY-4.0

CREATE TABLE orders (
  id      INTEGER PRIMARY KEY,
  country TEXT    NOT NULL,
  amount  INTEGER NOT NULL
);

INSERT INTO orders (id, country, amount) VALUES
  (1, 'de', 120),
  (2, 'fr', 80),
  (3, 'de', 45);
