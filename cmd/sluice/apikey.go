// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/secrets"
)

// newAPIKeyCmd exposes helpers operators need to issue / rotate API keys
// without running the server. The only subcommand today is `hash`, which
// computes the HMAC value that a SubjectBinding's hashRef must resolve
// to.
func newAPIKeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apikey",
		Short: "Manage API-key hashes",
	}
	cmd.AddCommand(newAPIKeyHashCmd())
	return cmd
}

// newAPIKeyHashCmd prints the hex-encoded HMAC-SHA256 of
// (keyID + ":" + material) under the supplied pepper. This matches the
// value identity.APIKeyIdentifier computes on the request path, so the
// output can be stored verbatim in a secret:// provider that backs a
// SubjectBinding apiKeys[].hashRef.
func newAPIKeyHashCmd() *cobra.Command {
	var (
		pepper   string
		keyID    string
		material string
	)

	cmd := &cobra.Command{
		Use:   "hash",
		Short: "Compute the hex HMAC for an API key",
		Long: `Compute the hex-encoded HMAC-SHA256(pepper, keyID + ":" + material)
that backs a SubjectBinding apiKeys[].hashRef.

The --pepper flag accepts either a raw string or a secret:// URI
(env, file). When given a URI it is resolved through the same provider
stack the server uses.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if keyID == "" || material == "" || pepper == "" {
				return &exitError{Code: 1, Msg: "apikey hash: --pepper, --id, and --material are required"}
			}
			pepperBytes, err := resolvePepper(cmd.Context(), pepper)
			if err != nil {
				return &exitError{Code: 1, Err: fmt.Errorf("resolve pepper: %w", err)}
			}
			sum := identity.ComputeHash(pepperBytes, keyID, material)
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), hex.EncodeToString(sum))
			return nil
		},
	}

	cmd.Flags().StringVar(&pepper, "pepper", "", "server pepper — raw value or secret:// URI")
	cmd.Flags().StringVar(&keyID, "id", "", "public key identifier")
	cmd.Flags().StringVar(&material, "material", "", "key material presented by the caller")
	return cmd
}

// resolvePepper returns the pepper bytes. A value that starts with
// "secret://" is resolved through the default resolver; anything else
// is taken verbatim.
func resolvePepper(ctx context.Context, v string) ([]byte, error) {
	if !strings.HasPrefix(v, "secret://") {
		return []byte(v), nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r := secrets.NewResolver(secrets.ResolverOptions{})
	return r.Resolve(ctx, v)
}
