// SPDX-License-Identifier: Apache-2.0

package errors

import "net/http"

// Code is a stable, string-typed error identifier. Clients key off these
// values; they are part of the public contract and must not be silently
// renamed. Additions require a changelog entry.
type Code string

// The complete set of client-facing error codes. New codes must be added
// here, given an entry in httpStatusByCode and defaultMessage, and
// documented in CHANGELOG.md.
const (
	// Syntax / parser
	CodeSyntax             Code = "ERR_SYNTAX"
	CodeMultipleStatements Code = "ERR_MULTIPLE_STATEMENTS"
	CodeUnsupportedSyntax  Code = "ERR_UNSUPPORTED_SYNTAX"
	CodeRewriteFailed      Code = "ERR_REWRITE_FAILED"
	CodeMaskContext        Code = "ERR_MASK_UNSUPPORTED_CONTEXT"

	// Identity / authorization
	CodeUnauthorized      Code = "ERR_UNAUTHORIZED"
	CodeForbidden         Code = "ERR_FORBIDDEN"
	CodeACLDenied         Code = "ACL_DENIED"
	CodeACLRejected       Code = "ACL_REJECTED"
	CodeInsufficientScope Code = "ERR_INSUFFICIENT_SCOPE"

	// Resource / configuration
	CodeDataSourceUnavailable Code = "ERR_DATASOURCE_UNAVAILABLE"
	CodeConfigInvalid         Code = "ERR_CONFIG_INVALID"
	CodePolicyInvalid         Code = "ERR_POLICY_INVALID"

	// Execution
	CodeTimeout         Code = "ERR_TIMEOUT"
	CodeCanceled        Code = "ERR_CANCELED"
	CodeRateLimited     Code = "ERR_RATE_LIMITED"
	CodeBudgetExceeded  Code = "ERR_BUDGET_EXCEEDED"
	CodePayloadTooLarge Code = "ERR_PAYLOAD_TOO_LARGE"
	CodeResultTruncated Code = "ERR_RESULT_TRUNCATED"

	// Audit
	CodeAuditUnavailable Code = "ERR_AUDIT_UNAVAILABLE"

	// Catch-all
	CodeInternal Code = "ERR_INTERNAL"
)

// statusClientClosedRequest is the non-standard 499 status used by nginx and
// others for client-cancelled requests. There is no constant in net/http, so
// we name it here rather than littering numeric literals in the table.
const statusClientClosedRequest = 499

// httpStatusByCode maps every code to its HTTP status. CodeResultTruncated
// maps to 200 because it is a warning returned alongside a successful body,
// not an error.
var httpStatusByCode = map[Code]int{
	CodeSyntax:                http.StatusBadRequest,
	CodeMultipleStatements:    http.StatusBadRequest,
	CodeUnsupportedSyntax:     http.StatusBadRequest,
	CodeRewriteFailed:         http.StatusBadRequest,
	CodeMaskContext:           http.StatusBadRequest,
	CodeUnauthorized:          http.StatusUnauthorized,
	CodeForbidden:             http.StatusForbidden,
	CodeACLDenied:             http.StatusForbidden,
	CodeACLRejected:           http.StatusForbidden,
	CodeInsufficientScope:     http.StatusForbidden,
	CodeDataSourceUnavailable: http.StatusServiceUnavailable,
	CodeConfigInvalid:         http.StatusBadRequest,
	CodePolicyInvalid:         http.StatusBadRequest,
	CodeTimeout:               http.StatusGatewayTimeout,
	CodeCanceled:              statusClientClosedRequest,
	CodeRateLimited:           http.StatusTooManyRequests,
	CodeBudgetExceeded:        http.StatusTooManyRequests,
	CodePayloadTooLarge:       http.StatusRequestEntityTooLarge,
	CodeResultTruncated:       http.StatusOK,
	CodeAuditUnavailable:      http.StatusServiceUnavailable,
	CodeInternal:              http.StatusInternalServerError,
}

// defaultMessage maps every code to a generic, client-safe message.
// Callers may override via APIError.WithMessage but must not leak sensitive
// internals (SQL text, stack traces, file paths) in the replacement.
var defaultMessage = map[Code]string{
	CodeSyntax:                "SQL parse error",
	CodeMultipleStatements:    "only a single statement is allowed per request",
	CodeUnsupportedSyntax:     "SQL feature not supported",
	CodeRewriteFailed:         "policy rewrite failed",
	CodeMaskContext:           "a post-query masked column cannot appear in a filter, join, or expression",
	CodeUnauthorized:          "authentication required",
	CodeForbidden:             "access forbidden",
	CodeACLDenied:             "access denied by policy",
	CodeACLRejected:           "query shape rejected by policy",
	CodeInsufficientScope:     "token lacks required scope",
	CodeDataSourceUnavailable: "data source unavailable",
	CodeConfigInvalid:         "server configuration invalid",
	CodePolicyInvalid:         "policy document invalid",
	CodeTimeout:               "request timed out",
	CodeCanceled:              "request canceled",
	CodeRateLimited:           "too many requests",
	CodeBudgetExceeded:        "query budget exceeded",
	CodePayloadTooLarge:       "payload too large",
	CodeResultTruncated:       "result truncated",
	CodeAuditUnavailable:      "audit log unavailable; request refused (fail-closed)",
	CodeInternal:              "internal error",
}

// Status returns the HTTP status associated with the code. Unknown codes
// map to 500 so a misused constant cannot accidentally return success.
func Status(c Code) int {
	if s, ok := httpStatusByCode[c]; ok {
		return s
	}
	return http.StatusInternalServerError
}

// Message returns the canonical, client-safe message for the code. Unknown
// codes fall back to the CodeInternal message.
func Message(c Code) string {
	if m, ok := defaultMessage[c]; ok {
		return m
	}
	return defaultMessage[CodeInternal]
}

// AllCodes lists every declared code. Used by tests and tooling.
func AllCodes() []Code {
	return []Code{
		CodeSyntax,
		CodeMultipleStatements,
		CodeUnsupportedSyntax,
		CodeRewriteFailed,
		CodeMaskContext,
		CodeUnauthorized,
		CodeForbidden,
		CodeACLDenied,
		CodeACLRejected,
		CodeInsufficientScope,
		CodeDataSourceUnavailable,
		CodeConfigInvalid,
		CodePolicyInvalid,
		CodeTimeout,
		CodeCanceled,
		CodeRateLimited,
		CodeBudgetExceeded,
		CodePayloadTooLarge,
		CodeResultTruncated,
		CodeAuditUnavailable,
		CodeInternal,
	}
}
