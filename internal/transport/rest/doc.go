// SPDX-License-Identifier: AGPL-3.0-or-later

// Package rest exposes the HTTP data-plane: POST /v1/query, GET /v1/health,
// GET /v1/ready, GET /v1/version, GET /openapi.json. Every business path
// routes through queryservice.Service; this package only handles wire
// encoding, content negotiation, timeouts, and middleware.
package rest
