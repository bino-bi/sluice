// SPDX-License-Identifier: AGPL-3.0-or-later

// Package audit records every query, policy decision, config reload,
// authentication failure, datasource lifecycle event, and admin action as a
// hash-chained JSON line. Records are written through a bounded queue into
// one or more Sinks; a File sink with daily + size rotation is shipped for
// the MVP, and the hash chain can be verified offline via the Verify walker
// (surfaced to operators through `sluice audit verify <dir>`).
//
// Canonical JSON determines hash stability: serialisation sorts map keys and
// uses no whitespace. Tampering with any byte of a past record breaks the
// chain at the exact record where it occurred.
package audit
