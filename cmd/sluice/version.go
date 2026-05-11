// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/bino-bi/sluice/internal/version"
)

func newVersionCmd() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print sluice build identity",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			b := version.Current()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(b); err != nil {
					return &exitError{Code: 1, Err: err}
				}
				return nil
			}
			_, _ = fmt.Fprintln(out, b.String())
			return nil
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "emit version metadata as JSON")
	return cmd
}
