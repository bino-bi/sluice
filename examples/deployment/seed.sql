-- SPDX-License-Identifier: CC-BY-4.0

CREATE TABLE orders (
    id           INTEGER PRIMARY KEY,
    customer     TEXT    NOT NULL,
    amount_cents INTEGER NOT NULL
);

INSERT INTO orders VALUES
    (1, 'acme', 1999),
    (2, 'beta', 4599);
