// SPDX-License-Identifier: AGPL-3.0-or-later

// Package motherduck attaches a MotherDuck database via DuckDB's
// motherduck extension. The driver:
//
//   - INSTALL/LOAD motherduck,
//   - resolves tokenRef via the SecretResolver and sets
//     `motherduck_token`, which holds for the whole DuckDB instance,
//   - runs ATTACH 'md:<database>' AS <catalog> (TYPE MOTHERDUCK, READ_ONLY).
//
// MotherDuck is the one MVP driver that talks to a hosted service. The
// integration test is gated on a MOTHERDUCK_TOKEN env var so forks
// without the token still pass the nightly matrix.
package motherduck
