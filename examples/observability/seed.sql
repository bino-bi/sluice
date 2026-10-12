-- SPDX-License-Identifier: CC-BY-4.0

CREATE TABLE customers (
    id   INTEGER PRIMARY KEY,
    name TEXT    NOT NULL
);

INSERT INTO customers VALUES
    (1, 'Alice'),
    (2, 'Bob');
