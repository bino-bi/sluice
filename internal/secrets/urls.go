// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"fmt"
	"net/url"
	"strings"
)

// URI is a parsed secret reference of the form `secret://<provider>/<path>[#fragment]`.
// The provider name lives in the URL host; everything after the host is the
// provider-specific path. Fragment is an optional field selector consumed by
// providers like vault/aws-sm for JSON-encoded secrets.
type URI struct {
	Scheme   string     // always "secret"
	Provider string     // "env" or "file" (vault/aws-sm/gcp-sm reserved, rejected)
	Path     string     // leading slash preserved for absolute-path semantics
	Fragment string     // optional
	Query    url.Values // optional
	Raw      string     // original input
}

// unimplementedProviders are reserved by the URI grammar but have no
// resolver in this build. Parse rejects them so misconfiguration fails at
// load, not at first resolve. Remove entries as providers land.
var unimplementedProviders = map[string]struct{}{
	"vault":  {},
	"aws-sm": {},
	"gcp-sm": {},
}

// Parse accepts references in the canonical form documented on
// pkg/datasource.SecretResolver and pkg/mask.SaltStore:
//
//	secret://env/VAR_NAME
//	secret://file//absolute/path                 (double slash: authority empty → file:///path)
//	secret://file/rel/or/abs/path                (single slash: leading slash preserved in Path)
//
// The vault, aws-sm, and gcp-sm forms are reserved for later releases and
// are rejected until their providers exist.
func Parse(raw string) (URI, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return URI{}, fmt.Errorf("secrets: empty URI")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return URI{}, fmt.Errorf("secrets: parse %q: %w", raw, err)
	}
	if u.Scheme != "secret" {
		return URI{}, fmt.Errorf("secrets: scheme %q is not %q", u.Scheme, "secret")
	}
	if u.Host == "" {
		return URI{}, fmt.Errorf("secrets: %q missing provider (expected secret://<provider>/...)", raw)
	}
	if _, ok := unimplementedProviders[u.Host]; ok {
		return URI{}, fmt.Errorf(
			"secrets: provider %q parsed but unimplemented — only env and file resolve in this build (docs/operations/server-config.md)", u.Host)
	}

	return URI{
		Scheme:   u.Scheme,
		Provider: u.Host,
		Path:     u.Path,
		Fragment: u.Fragment,
		Query:    u.Query(),
		Raw:      raw,
	}, nil
}

// Name returns the path without its leading slash — useful for env var names
// where the path is a single identifier.
func (u URI) Name() string {
	return strings.TrimPrefix(u.Path, "/")
}
