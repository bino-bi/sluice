// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/bino-bi/sluice/internal/budget"
)

// newBudgetCmd groups `sluice budget …` subcommands.
func newBudgetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "budget",
		Short: "Inspect per-subject query budgets",
	}
	cmd.AddCommand(newBudgetShowCmd())
	return cmd
}

func newBudgetShowCmd() *cobra.Command {
	var (
		stateDir string
		day      string
		asJSON   bool
	)
	cmd := &cobra.Command{
		Use:   "show <subject>",
		Short: "Show a subject's budget usage for a day (default today, UTC)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			subject := args[0]
			mgr, err := budget.New(budget.Options{Path: filepath.Join(stateDir, "budget.db")})
			if err != nil {
				return &exitError{Code: 1, Err: err}
			}
			defer func() { _ = mgr.Close(context.Background()) }()

			u, err := mgr.Usage(cmd.Context(), subject, day)
			if err != nil {
				return &exitError{Code: 1, Err: err}
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), map[string]any{
					"subject": subject, "cpu_seconds": u.CPUSecondsPerDay, "rows": u.RowsPerDay,
				})
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(),
				"subject %s: cpu_seconds=%d rows=%d\n", subject, u.CPUSecondsPerDay, u.RowsPerDay)
			return nil
		},
	}
	cmd.Flags().StringVar(&stateDir, "state-dir", "./state", "budget state directory")
	cmd.Flags().StringVar(&day, "day", "", "UTC day (YYYY-MM-DD); default today")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	return cmd
}
