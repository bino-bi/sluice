-- SPDX-License-Identifier: CC-BY-4.0

CREATE TABLE customers (
  id      INTEGER PRIMARY KEY,
  name    TEXT NOT NULL,
  country TEXT NOT NULL
);

-- No policy allows payroll. Queries on it are denied.
CREATE TABLE payroll (
  id     INTEGER PRIMARY KEY,
  name   TEXT NOT NULL,
  salary INTEGER NOT NULL
);

INSERT INTO customers VALUES
  (1, 'Alice', 'DE'),
  (2, 'Bob',   'FR'),
  (3, 'Chen',  'DE');

INSERT INTO payroll VALUES
  (1, 'Dana', 5200),
  (2, 'Erin', 4800);
