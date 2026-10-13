-- SPDX-License-Identifier: CC-BY-4.0
-- Transactional data. serve.sh loads it into the SQLite file data/shop.db.

CREATE TABLE orders (
    id           INTEGER PRIMARY KEY,
    customer_id  INTEGER NOT NULL,
    country_code TEXT    NOT NULL,
    amount_cents INTEGER NOT NULL
);

INSERT INTO orders VALUES
    (1, 101, 'US',  1999),
    (2, 102, 'DE',  4599),
    (3, 103, 'FR',  7800),
    (4, 104, 'US',   250),
    (5, 105, 'JP', 32000);
