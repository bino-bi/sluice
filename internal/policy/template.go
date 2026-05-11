// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"fmt"
	"net"
	"strings"

	"github.com/bino-bi/sluice/internal/identity"
)

// Template is a parsed `{{ … }}` expression. Render resolves the
// variable against a UserCtx and optional request-scope facts and returns
// the fully-materialised value. The rewriter then appends the value as a
// positional parameter and emits a `$N` placeholder — no user data ever
// reaches the final SQL as a literal.
type Template struct {
	// Path is the dotted variable reference, e.g. ["subject", "jwt",
	// "tenant_id"] for `{{ subject.jwt.tenant_id }}`.
	Path []string
	// Raw is the original template string, preserved for diagnostics.
	Raw string
}

// looksLikeTemplate reports whether s appears to contain a template
// expression. The check is cheap and conservative — the full parse
// happens inside CompileTemplate.
func looksLikeTemplate(s string) bool {
	return strings.Contains(s, "{{") && strings.Contains(s, "}}")
}

// CompileTemplate parses a `{{ path.to.var }}` string. The MVP supports a
// single variable reference spanning the entire value — mixed
// literal+template strings (`prefix-{{ subject.sub }}-suffix`) are not
// supported.
func CompileTemplate(s string) (*Template, error) {
	if !looksLikeTemplate(s) {
		return nil, fmt.Errorf("%w: %q does not contain {{…}}", ErrTemplateInvalid, s)
	}
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "{{") || !strings.HasSuffix(trimmed, "}}") {
		return nil, fmt.Errorf("%w: %q: mixed literal/template not supported", ErrTemplateInvalid, s)
	}
	inner := strings.TrimSpace(trimmed[2 : len(trimmed)-2])
	if inner == "" {
		return nil, fmt.Errorf("%w: empty variable reference", ErrTemplateInvalid)
	}
	parts := strings.Split(inner, ".")
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			return nil, fmt.Errorf("%w: empty path segment in %q", ErrTemplateInvalid, s)
		}
	}
	return &Template{Path: parts, Raw: s}, nil
}

// RequestFacts carries the per-request attributes addressable from
// templates and, later, from CEL.
type RequestFacts struct {
	RemoteIP  net.IP
	UserAgent string
	Headers   map[string]string
}

// Render resolves the template against user + facts. Recognised roots:
//
//   - subject.*     — UserCtx fields (subject, issuer, email, request_id,
//     auth_method) and nested claims via subject.jwt.<path> /
//     subject.claims.<path>.
//   - subject.groups — returns the []string.
//   - request.*     — facts (remote_ip, user_agent, headers.<name>).
//
// Unknown roots or missing intermediate fields return ErrTemplateVarMissing.
func (t *Template) Render(user *identity.UserCtx, facts *RequestFacts) (any, error) {
	if t == nil || len(t.Path) == 0 {
		return nil, fmt.Errorf("%w: nil template", ErrTemplateInvalid)
	}
	switch t.Path[0] {
	case "subject":
		return renderSubject(user, t.Path[1:])
	case "request":
		return renderRequest(facts, t.Path[1:])
	default:
		return nil, fmt.Errorf("%w: unknown root %q in %q", ErrTemplateVarMissing, t.Path[0], t.Raw)
	}
}

func renderSubject(user *identity.UserCtx, path []string) (any, error) {
	if user == nil {
		return nil, fmt.Errorf("%w: no authenticated subject", ErrTemplateVarMissing)
	}
	if len(path) == 0 {
		return nil, fmt.Errorf("%w: subject requires a sub-path", ErrTemplateVarMissing)
	}
	head, rest := path[0], path[1:]
	switch head {
	case "sub", "subject":
		if len(rest) != 0 {
			return nil, fmt.Errorf("%w: subject.sub does not have sub-fields", ErrTemplateVarMissing)
		}
		return user.Subject, nil
	case "issuer":
		return user.Issuer, nil
	case "email":
		return user.Email, nil
	case "groups":
		return user.Groups, nil
	case "auth_method":
		return string(user.AuthMethod), nil
	case "request_id":
		return user.RequestID, nil
	case "jwt", "claims":
		return walkClaims(user.Claims, rest)
	default:
		// Fall through to raw claim lookup for convenience.
		return walkClaims(user.Claims, path)
	}
}

func renderRequest(facts *RequestFacts, path []string) (any, error) {
	if facts == nil {
		return nil, fmt.Errorf("%w: request facts unavailable", ErrTemplateVarMissing)
	}
	if len(path) == 0 {
		return nil, fmt.Errorf("%w: request requires a sub-path", ErrTemplateVarMissing)
	}
	switch path[0] {
	case "remote_ip":
		if len(path) != 1 {
			return nil, fmt.Errorf("%w: request.remote_ip is a leaf", ErrTemplateVarMissing)
		}
		if facts.RemoteIP == nil {
			return nil, fmt.Errorf("%w: request.remote_ip not populated", ErrTemplateVarMissing)
		}
		return facts.RemoteIP.String(), nil
	case "user_agent":
		if len(path) != 1 {
			return nil, fmt.Errorf("%w: request.user_agent is a leaf", ErrTemplateVarMissing)
		}
		return facts.UserAgent, nil
	case "headers":
		if len(path) != 2 {
			return nil, fmt.Errorf("%w: request.headers requires exactly one key", ErrTemplateVarMissing)
		}
		v, ok := facts.Headers[path[1]]
		if !ok {
			return nil, fmt.Errorf("%w: request.headers[%q] missing", ErrTemplateVarMissing, path[1])
		}
		return v, nil
	}
	return nil, fmt.Errorf("%w: unknown request field %q", ErrTemplateVarMissing, path[0])
}

// walkClaims descends a nested map[string]any via the given path.
func walkClaims(claims map[string]any, path []string) (any, error) {
	if len(path) == 0 {
		return nil, fmt.Errorf("%w: claims requires a sub-path", ErrTemplateVarMissing)
	}
	var current any = claims
	for i, seg := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: cannot descend into %q (segment %d is not an object)",
				ErrTemplateVarMissing, seg, i)
		}
		next, found := m[seg]
		if !found {
			return nil, fmt.Errorf("%w: claim path segment %q missing", ErrTemplateVarMissing, seg)
		}
		current = next
	}
	return current, nil
}
