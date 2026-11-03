-- SPDX-License-Identifier: CC-BY-4.0
-- 2000 orders from fixed formulas, so every run gives the same data.

CREATE TABLE orders (
  id        INTEGER PRIMARY KEY,
  region    TEXT    NOT NULL,
  product   TEXT    NOT NULL,
  amount    REAL    NOT NULL,
  placed_on DATE    NOT NULL,
  email     TEXT    NOT NULL
);

WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 2000)
INSERT INTO orders
SELECT
  i,
  CASE WHEN i % 10 < 4 THEN 'north'
       WHEN i % 10 < 7 THEN 'south'
       WHEN i % 10 < 9 THEN 'east'
       ELSE 'west' END,
  CASE WHEN i % 667 = 0 THEN 'gift card'
       WHEN i % 5 < 2 THEN 'coffee'
       WHEN i % 5 < 4 THEN 'tea'
       ELSE 'mug' END,
  round(5 + (i * 37 % 1000) * (i * 37 % 1000) / 4000.0, 2),
  date('2026-01-01', '+' || (i * 7 % 300) || ' days'),
  'customer' || (i * 13 % 900) || '@example.org'
FROM n;
