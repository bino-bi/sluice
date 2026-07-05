// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/policytest"
)

// newPolicyTestCmd runs declarative policy test suites against a policy
// directory. Exit codes mirror `policy validate`:
//
//	0  all cases pass
//	1  I/O / flag error
//	3  compile failure or one or more case failures
func newPolicyTestCmd() *cobra.Command {
	var (
		testsPath string
		strict    bool
		asJSON    bool
	)

	cmd := &cobra.Command{
		Use:   "test <policy-dir>",
		Short: "Run declarative policy test suites against <policy-dir>",
		Long: `Test compiles the policies under <policy-dir> and runs every test case
in the suite files, asserting the resulting outcome, row filters, column
masks, post-query masks, applied policies, and rewritten SQL.

Suite files default to <policy-dir>/tests/*.yaml; override with --tests
(a file or directory).

Exit codes:
  0  all cases pass
  1  I/O or flag error
  3  compile failure or case failure`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			policyDir := args[0]

			runner, err := policytest.NewRunner(ctx, policyDir, strict)
			if err != nil {
				var verrs config.ValidationErrors
				if errors.As(err, &verrs) {
					for _, ve := range verrs {
						_, _ = fmt.Fprintln(cmd.ErrOrStderr(), ve.Error())
					}
					return &exitError{Code: 3}
				}
				return &exitError{Code: 1, Err: err}
			}

			tests := testsPath
			if tests == "" {
				tests = filepath.Join(policyDir, "tests")
			}
			info, err := os.Stat(tests)
			if err != nil {
				return &exitError{Code: 1, Err: fmt.Errorf("locate tests %q: %w", tests, err)}
			}

			var report *policytest.Report
			if info.IsDir() {
				report, err = runner.RunDir(ctx, tests)
			} else {
				report, err = runner.RunFile(ctx, tests)
			}
			if err != nil {
				return &exitError{Code: 1, Err: err}
			}

			if asJSON {
				if err := writeJSON(cmd.OutOrStdout(), report); err != nil {
					return &exitError{Code: 1, Err: err}
				}
			} else {
				writePolicyTestReport(cmd.OutOrStdout(), report)
			}
			if report.Failed > 0 || report.Total == 0 {
				return &exitError{Code: 3}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&testsPath, "tests", "", "suite file or directory (default <policy-dir>/tests)")
	cmd.Flags().BoolVar(&strict, "strict", false, "reject unknown YAML fields in policies")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the report as JSON")
	return cmd
}

func writePolicyTestReport(w interface{ Write([]byte) (int, error) }, r *policytest.Report) {
	for _, c := range r.Cases {
		status := "PASS"
		if !c.Passed {
			status = "FAIL"
		}
		_, _ = fmt.Fprintf(w, "%s  %s\n", status, c.Name)
		for _, f := range c.Failures {
			_, _ = fmt.Fprintf(w, "        %s\n", f)
		}
	}
	_, _ = fmt.Fprintf(w, "\n%d passed, %d failed, %d total\n", r.Passed, r.Failed, r.Total)
}
