// SPDX-License-Identifier: AGPL-3.0-or-later

// Package telemetry is the single integration point for structured logging
// (log/slog), Prometheus metrics, and OpenTelemetry tracing. The MVP slice
// wires slog and Prometheus; OTel tracing arrives with the query-path slice.
package telemetry
