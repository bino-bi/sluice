-- SPDX-License-Identifier: CC-BY-4.0
-- Seed data for the hello-sluice example.
-- Run: sqlite3 data/shop.db < seed.sql

DROP TABLE IF EXISTS customers;
DROP TABLE IF EXISTS orders;
DROP TABLE IF EXISTS admin_users;

CREATE TABLE customers (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL,
    email      TEXT    NOT NULL,
    tenant_id  TEXT    NOT NULL,
    created_at TEXT    NOT NULL
);

CREATE TABLE orders (
    id         INTEGER PRIMARY KEY,
    customer_id INTEGER NOT NULL,
    total_cents INTEGER NOT NULL,
    tenant_id  TEXT    NOT NULL,
    placed_at  TEXT    NOT NULL
);

-- admin_users is intentionally excluded from the allow policy; queries
-- against it should be rejected with ERR_ACL_DENIED.
CREATE TABLE admin_users (
    id       INTEGER PRIMARY KEY,
    username TEXT    NOT NULL,
    role     TEXT    NOT NULL
);

INSERT INTO customers VALUES
    (1, 'Alice',   'alice@acme.test',   'acme',   '2026-01-03T10:00:00Z'),
    (2, 'Bob',     'bob@acme.test',     'acme',   '2026-01-04T11:30:00Z'),
    (3, 'Charlie', 'charlie@widget.io', 'widget', '2026-01-05T09:15:00Z'),
    (4, 'Dana',    'dana@widget.io',    'widget', '2026-01-06T16:45:00Z'),
    (5, 'Erin',    'erin@globex.biz',   'globex', '2026-01-07T08:00:00Z');

INSERT INTO orders VALUES
    (101, 1, 12900, 'acme',   '2026-03-01T12:00:00Z'),
    (102, 1, 4500,  'acme',   '2026-03-02T14:20:00Z'),
    (103, 2, 19900, 'acme',   '2026-03-05T10:10:00Z'),
    (201, 3, 7800,  'widget', '2026-03-04T11:00:00Z'),
    (202, 4, 23400, 'widget', '2026-03-06T13:30:00Z'),
    (301, 5, 5600,  'globex', '2026-03-07T09:45:00Z');

INSERT INTO admin_users VALUES
    (1, 'root',  'superuser'),
    (2, 'alice', 'maintainer');
