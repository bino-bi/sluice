-- SPDX-License-Identifier: CC-BY-4.0

CREATE TABLE customers (
  id    INTEGER PRIMARY KEY,
  name  TEXT NOT NULL,
  email TEXT NOT NULL,
  phone TEXT NOT NULL
);

CREATE TABLE suppliers (
  id    INTEGER PRIMARY KEY,
  name  TEXT NOT NULL,
  email TEXT NOT NULL
);

CREATE TABLE payroll (
  id         INTEGER PRIMARY KEY,
  name       TEXT    NOT NULL,
  salary_eur INTEGER NOT NULL
);

INSERT INTO customers (id, name, email, phone) VALUES
  (1, 'Anna',  'anna@example.com',  '+49-30-1234567'),
  (2, 'Bernd', 'bernd@example.com', '+49-89-7654321');

INSERT INTO suppliers (id, name, email) VALUES
  (1, 'Paper Ltd', 'sales@paper.example');

INSERT INTO payroll (id, name, salary_eur) VALUES
  (1, 'Anna', 72000);
