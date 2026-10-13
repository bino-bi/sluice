-- SPDX-License-Identifier: CC-BY-4.0

DROP TABLE IF EXISTS customers;
DROP TABLE IF EXISTS admin_users;

CREATE TABLE customers (
    id    INTEGER PRIMARY KEY,
    name  TEXT NOT NULL,
    email TEXT NOT NULL
);

-- No policy allows admin_users.
CREATE TABLE admin_users (
    id       INTEGER PRIMARY KEY,
    username TEXT NOT NULL
);

INSERT INTO customers VALUES
    (1, 'Alice', 'alice@acme.example'),
    (2, 'Bob',   'bob@acme.example');

INSERT INTO admin_users VALUES
    (1, 'root');
