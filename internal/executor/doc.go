// SPDX-License-Identifier: AGPL-3.0-or-later

// Package executor owns the embedded DuckDB instance. It provides a
// hardened connection pool, runs already-rewritten SQL with parameters,
// streams results in JSON or CSV (Arrow lands in a later slice), and
// enforces statement timeouts and row caps.
//
// Hardening (applied on every fresh connection, in order): disable
// external access, forbid community extensions, disable autoload /
// autoinstall, disallow persistent secrets, apply user tunables
// (memory_limit, threads, temp_directory), set statement_timeout, then
// finally lock_configuration to freeze the above for the life of the
// connection.
//
// The composition root (cmd/sluice serve) wires the executor together
// with the datasource.Registry: the registry's AttachHook runs on every
// new connection so inbound queries see every catalog attached.
package executor
