// SPDX-License-Identifier: AGPL-3.0-or-later

// Package config loads the sluice server configuration and the policy
// directory, producing an immutable Snapshot consumed by the rest of the
// system. Hot-reload and fsnotify watching are deferred to a later slice;
// this package implements one-shot load only.
package config
