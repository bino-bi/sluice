-- SPDX-License-Identifier: CC-BY-4.0

CREATE TABLE products (
  id          INTEGER PRIMARY KEY,
  name        TEXT    NOT NULL,
  price_cents INTEGER NOT NULL
);

INSERT INTO products VALUES
  (1, 'Ceramic Mug',   1299),
  (2, 'A5 Notebook',    799),
  (3, 'Black Pen',      199),
  (4, 'Sticker Pack',   999);

-- A deny policy covers this table.
CREATE TABLE salaries (
  employee TEXT    NOT NULL,
  amount   INTEGER NOT NULL
);

INSERT INTO salaries VALUES
  ('anna',  5200),
  ('bernd', 4800);
