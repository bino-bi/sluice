-- SPDX-License-Identifier: CC-BY-4.0

CREATE TABLE IF NOT EXISTS employees (
  id         INTEGER PRIMARY KEY,
  name       TEXT    NOT NULL,
  phone      TEXT    NOT NULL,
  birth_date TEXT    NOT NULL,
  address    TEXT    NOT NULL,
  ssn        TEXT    NOT NULL,
  card       TEXT    NOT NULL,
  salary     INTEGER NOT NULL,
  notes      TEXT    NOT NULL,
  email      TEXT    NOT NULL
);

DELETE FROM employees;

-- --8<-- [start:rows]
INSERT INTO employees (id, name, phone, birth_date, address, ssn, card, salary, notes, email) VALUES
  (1, 'Alice Martin', '+1-555-0101', '1988-03-14', '12 Oak Street', '111-22-3333',
   '4111-1111-1111-1111', 72000, 'Prefers calls in the morning', 'alice@example.com'),
  (2, 'Bob Keller', '+1-555-0102', '1975-07-22', '7 Hill Road', '222-33-4444',
   '5500-0000-0000-0004', 58000, 'On leave', 'bob@example.com');
-- --8<-- [end:rows]
