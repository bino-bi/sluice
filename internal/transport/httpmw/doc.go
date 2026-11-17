// SPDX-License-Identifier: AGPL-3.0-or-later

// Package httpmw holds the HTTP middleware and the shutdown drain that
// the REST, MCP and admin listeners share: body cap, request timeout,
// request id, panic recovery and the rate limit ahead of the login. One
// copy keeps the three chains from drifting apart.
package httpmw
