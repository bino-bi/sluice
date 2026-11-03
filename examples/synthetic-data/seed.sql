-- SPDX-License-Identifier: CC-BY-4.0
-- 400 customers and 2000 orders from fixed formulas, so every run gives the same data.

CREATE TABLE customers (
  id         INTEGER PRIMARY KEY,
  first_name TEXT    NOT NULL,
  last_name  TEXT    NOT NULL,
  city       TEXT    NOT NULL,
  email      TEXT    NOT NULL
);

WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 400)
INSERT INTO customers (id, first_name, last_name, city, email)
SELECT
  i,
  CASE i % 8 WHEN 0 THEN 'Anna' WHEN 1 THEN 'Bernd' WHEN 2 THEN 'Clara' WHEN 3 THEN 'Dieter'
             WHEN 4 THEN 'Erika' WHEN 5 THEN 'Frieda' WHEN 6 THEN 'Gustav' ELSE 'Heinz' END,
  CASE (i / 8) % 8 WHEN 0 THEN 'Schmidt' WHEN 1 THEN 'Mueller' WHEN 2 THEN 'Schulz' WHEN 3 THEN 'Wagner'
                   WHEN 4 THEN 'Becker' WHEN 5 THEN 'Hoffmann' WHEN 6 THEN 'Koch' ELSE 'Richter' END,
  CASE i % 4 WHEN 0 THEN 'Hamburg' WHEN 1 THEN 'Bremen' WHEN 2 THEN 'Kiel' ELSE 'Essen' END,
  'customer' || i || '@example.org'
FROM n;

-- Customer 1 has 60 orders, customers 2 to 389 have 5 each, customers 390 to 400 have none.
CREATE TABLE orders (
  id          INTEGER PRIMARY KEY,
  customer_id INTEGER NOT NULL,
  region      TEXT    NOT NULL,
  product     TEXT    NOT NULL,
  amount      REAL    NOT NULL,
  quantity    INTEGER NOT NULL,
  placed_on   DATE    NOT NULL,
  shipped_on  DATE    NOT NULL
);

WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 2000)
INSERT INTO orders
SELECT
  i,
  CASE WHEN i <= 60 THEN 1 ELSE 2 + (i - 61) % 388 END,
  CASE WHEN i % 10 < 4 THEN 'north'
       WHEN i % 10 < 7 THEN 'south'
       WHEN i % 10 < 9 THEN 'east'
       ELSE 'west' END,
  CASE WHEN i % 667 = 0 THEN 'gift card'
       WHEN i % 5 < 2 THEN 'coffee'
       WHEN i % 5 < 4 THEN 'tea'
       ELSE 'mug' END,
  round(5 + (i * 37 % 1000) * (i * 37 % 1000) / 4000.0, 2),
  1 + (i * 37 % 1000) / 125 + i % 3,
  date('2026-01-01', '+' || (i * 7 % 300) || ' days'),
  date('2026-01-01', '+' || (i * 7 % 300 + 1 + i % 9) || ' days')
FROM n;
