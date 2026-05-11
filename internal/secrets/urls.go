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
	Provider string     // "env", "file", "vault", "aws-sm", "gcp-sm"
	Path     string     // leading slash preserved for absolute-path semantics
	Fragment string     // optional
	Query    url.Values // optional
	Raw      string     // original input
}

// Parse accepts references in the canonical form documented on
// pkg/datasource.SecretResolver and pkg/mask.SaltStore:
//
//	secret://env/VAR_NAME
//	secret://file//absolute/path                 (double slash: authority empty → file:///path)
//	secret://file/rel/or/abs/path                (single slash: leading slash preserved in Path)
//	secret://vault/secret/data/pii#value
//	secret://aws-sm/prod/sluice/pii#salt
//	secret://gcp-sm/projects/demo/secrets/pii/versions/latest
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
