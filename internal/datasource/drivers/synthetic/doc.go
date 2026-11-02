// SPDX-License-Identifier: AGPL-3.0-or-later

// Package synthetic attaches the current snapshot of a synthetic data
// source. A build (internal/synth/build) writes the snapshot; this
// driver only reads it. Without a snapshot the catalog is an empty
// in-memory database, so queries see no tables instead of an error.
package synthetic
