// SPDX-License-Identifier: AGPL-3.0-or-later

// Package policy evaluates the active Sluice policy snapshot against a
// request and emits a Decision describing which row filters to inject,
// which column masks to apply, and whether the query is allowed, denied,
// or rejected. The package is pure: given identical inputs it produces
// identical outputs. CEL conditions are declared-only in MVP — non-empty
// Conditions blocks fail policy-load validation.
package policy
