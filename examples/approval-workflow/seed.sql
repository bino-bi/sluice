-- SPDX-License-Identifier: CC-BY-4.0
-- Sample data for the approval-workflow example. ssn and salary_eur are the sensitive columns.

DROP TABLE IF EXISTS people;

CREATE TABLE people (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL,
    country    TEXT    NOT NULL,
    ssn        TEXT    NOT NULL,
    salary_eur INTEGER NOT NULL
);

INSERT INTO people VALUES
    (1, 'Anna',  'de', '111-22-3333', 72000),
    (2, 'Bernd', 'de', '222-33-4444', 65000),
    (3, 'Chloe', 'fr', '333-44-5555', 58000);
