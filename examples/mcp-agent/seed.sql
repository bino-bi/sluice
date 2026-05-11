-- SPDX-License-Identifier: CC-BY-4.0
CREATE TABLE IF NOT EXISTS products (
  id       INTEGER PRIMARY KEY,
  sku      TEXT    NOT NULL,
  name     TEXT    NOT NULL,
  price_cents INTEGER NOT NULL,
  stock    INTEGER NOT NULL
);

DELETE FROM products;
INSERT INTO products VALUES
  (1, 'MUG-001',   'Ceramic Mug',       1299,  42),
  (2, 'TEE-BLK-M', 'Black T-Shirt (M)', 2499, 120),
  (3, 'NOTE-A5',   'A5 Notebook',        799, 200),
  (4, 'PEN-BLK',   'Black Ballpoint',    199, 500),
  (5, 'STICK-L',   'Sticker Pack (L)',   999,  80);
