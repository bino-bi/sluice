// SPDX-License-Identifier: AGPL-3.0-or-later

// Package identity turns inbound credentials (JWT bearer token, API key)
// into a UserCtx the rest of the pipeline keys off. Every transport
// funnels requests through an Identifier: REST + MCP streamable HTTP
// inspect headers, MCP stdio uses env vars, admin relies on a separate
// static-token identifier with its own bindings.
//
// MVP scope: JWT Bearer (HS/RS/ES 256/384) with JWKS cache + iss/aud/exp
// validation and claim-path extraction, plus API-Key (HMAC-SHA256 with
// server-wide pepper). A composite Identifier tries each registered
// method in order; the HTTP middleware populates UserCtx on success or
// emits 401 on invalid credentials. OIDC + mTLS are v1 additions.
package identity
