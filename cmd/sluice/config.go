// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/bino-bi/sluice/internal/config"
)

// newConfigCmd wires `sluice config validate`. `sluice config` itself is a
// grouping command — running it without a subcommand prints help and exits 0.
func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and validate sluice configuration",
	}
	cmd.AddCommand(newConfigValidateCmd())
	return cmd
}

func newConfigValidateCmd() *cobra.Command {
	var (
		serverPath string
		strict     bool
	)

	cmd := &cobra.Command{
		Use:   "validate <policy-dir>",
		Short: "Validate sluice server config and policy directory",
		Long: `Validate loads sluice.yaml (if --config is given) and every policy
manifest under <policy-dir>, surfacing all decoding or structural errors.

Exit codes:
  0  success
  1  I/O error (file not readable, missing flags, etc.)
  3  validation failure (one or more policy documents rejected, or server
     config setting controls this build cannot enforce)`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			if serverPath != "" {
				scfg, err := config.LoadServer(serverPath, nil)
				if err != nil {
					return &exitError{Code: 1, Err: fmt.Errorf("server config: %w", err)}
				}
				if verr := scfg.Validate(); verr != nil {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), verr.Error())
					return &exitError{Code: 3}
				}
				_, _ = fmt.Fprintf(out, "server config OK: %s\n", serverPath)
			}

			if len(args) == 0 {
				return nil
			}

			opts := config.LoadOptions{
				Strict:  strict,
				Sources: []config.SourceDir{{Path: args[0]}},
			}
			snap, err := config.LoadDirectory(context.Background(), opts)
			if err != nil {
				var verrs config.ValidationErrors
				if errors.As(err, &verrs) {
					for _, ve := range verrs {
						_, _ = fmt.Fprintln(cmd.ErrOrStderr(), ve.Error())
					}
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%d validation error(s)\n", len(verrs))
					return &exitError{Code: 3}
				}
				return &exitError{Code: 1, Err: err}
			}

			_, _ = fmt.Fprintf(out, "policies OK: %s (%d objects, digest %s)\n",
				args[0], len(snap.Policies), shortDigest(snap.Digest))
			return nil
		},
	}

	cmd.Flags().StringVar(&serverPath, "config", "", "path to sluice.yaml (optional)")
	cmd.Flags().BoolVar(&strict, "strict", false, "reject unknown YAML fields")

	return cmd
}

func shortDigest(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	return d
}
