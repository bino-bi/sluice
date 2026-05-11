// SPDX-License-Identifier: AGPL-3.0-or-later

// Package common holds helpers shared across driver implementations:
// identifier validation, SQL-string escaping, DuckDB CREATE SECRET
// rendering, and S3 path matching. It must not import any driver
// package — drivers import common, not the other way around.
package common
