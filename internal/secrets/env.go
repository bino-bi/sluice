// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"context"
	"fmt"
	"os"
)

// envProvider fetches secrets from process environment variables. URIs have
// the form secret://env/VAR_NAME; VAR_NAME is case-sensitive per POSIX.
type envProvider struct{}

// Scheme implements Provider.
func (envProvider) Scheme() string { return "env" }

// Fetch returns the value of the environment variable named by u.Name().
func (envProvider) Fetch(_ context.Context, u URI) ([]byte, error) {
	name := u.Name()
	if name == "" {
		return nil, fmt.Errorf("env: %q has no variable name", u.Raw)
	}
	val, ok := os.LookupEnv(name)
	if !ok {
		return nil, fmt.Errorf("env: %q not set", name)
	}
	return []byte(val), nil
}
