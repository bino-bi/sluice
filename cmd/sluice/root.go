// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"github.com/spf13/cobra"
)

// newRootCmd builds the cobra root command and attaches every subcommand.
// `serve` is the composition root; every other command is self-contained
// (read-only, no background goroutines).
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "sluice",
		Short:         "SQL access-policy gateway",
		SilenceUsage:  true, // usage is noise on operational errors
		SilenceErrors: true, // main decides how to format + exit
	}

	root.AddCommand(newVersionCmd())
	root.AddCommand(newConfigCmd())
	root.AddCommand(newSchemaCmd())
	root.AddCommand(newAuditCmd())
	root.AddCommand(newPolicyCmd())
	root.AddCommand(newDataSourceCmd())
	root.AddCommand(newAPIKeyCmd())
	root.AddCommand(newServeCmd())
	root.AddCommand(newMCPCmd())
	root.AddCommand(newBudgetCmd())

	return root
}
