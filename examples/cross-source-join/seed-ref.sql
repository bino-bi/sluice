-- SPDX-License-Identifier: CC-BY-4.0
-- Reference data. serve.sh loads it into the DuckDB file data/ref.duckdb.

CREATE TABLE countries (
    code   VARCHAR PRIMARY KEY,
    name   VARCHAR NOT NULL,
    region VARCHAR NOT NULL
);

INSERT INTO countries VALUES
    ('US', 'United States', 'Americas'),
    ('DE', 'Germany',       'EMEA'),
    ('FR', 'France',        'EMEA'),
    ('JP', 'Japan',         'APAC');
