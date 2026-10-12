-- SPDX-License-Identifier: CC-BY-4.0

CREATE TABLE orders (
    id           INTEGER PRIMARY KEY,
    customer_id  INTEGER NOT NULL,
    amount_cents INTEGER NOT NULL
);

CREATE TABLE customers (
    id   INTEGER PRIMARY KEY,
    name TEXT    NOT NULL
);

INSERT INTO orders VALUES
    (1, 1, 1999),
    (2, 2, 4599);

INSERT INTO customers VALUES
    (1, 'Alice'),
    (2, 'Bob');
