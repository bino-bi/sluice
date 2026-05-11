-- SPDX-License-Identifier: CC-BY-4.0
-- Seeded by the `postgres-init` service in docker-compose. Hosts the
-- transactional orders. The reference data (country codes) lives in
-- S3 Parquet instead, and Sluice joins the two through DuckDB.

CREATE TABLE IF NOT EXISTS orders (
  id          INTEGER PRIMARY KEY,
  customer_id INTEGER NOT NULL,
  country_code TEXT   NOT NULL,
  amount_cents INTEGER NOT NULL,
  created_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

INSERT INTO orders (id, customer_id, country_code, amount_cents) VALUES
  (1, 101, 'US', 1999),
  (2, 102, 'DE', 4599),
  (3, 103, 'FR', 7800),
  (4, 104, 'US',  250),
  (5, 105, 'JP', 32000)
ON CONFLICT DO NOTHING;
