// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/bino-bi/sluice/internal/audit"
)

// newAuditCmd groups the audit-related subcommands. `audit verify` is the
// only one in MVP; `audit tail` lives in the admin HTTP surface.
func newAuditCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Inspect sluice audit logs",
	}
	cmd.AddCommand(newAuditVerifyCmd())
	return cmd
}

func newAuditVerifyCmd() *cobra.Command {
	var (
		anchor string
		asJSON bool
	)

	cmd := &cobra.Command{
		Use:   "verify <audit-dir>",
		Short: "Validate the audit-log hash chain under <audit-dir>",
		Long: `Verify walks every *.jsonl file under <audit-dir> in filename order,
recomputes each record's SHA256, and carries the prior hash across file
boundaries. The optional --anchor pins the genesis record's prior_hash
so a replay attacker with a different installation seed cannot splice in
an unrelated chain.

Exit codes:
  0  chain intact
  1  I/O error (directory unreadable, malformed JSON)
  4  chain broken (tampering, missing record, or anchor mismatch)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rep, err := audit.Verify(args[0], anchor)
			if err != nil {
				var ve *audit.VerifyError
				if errors.As(err, &ve) {
					if asJSON {
						_ = json.NewEncoder(cmd.ErrOrStderr()).Encode(map[string]any{
							"ok":    false,
							"file":  ve.File,
							"line":  ve.Line,
							"error": ve.Msg,
						})
					} else {
						_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
					}
					return &exitError{Code: 4}
				}
				return &exitError{Code: 1, Err: err}
			}

			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
					"ok":        true,
					"files":     rep.Files,
					"records":   rep.Records,
					"last_hash": rep.LastHash,
				})
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(),
				"chain OK (%d file(s), %d record(s), last_hash=%s)\n",
				rep.Files, rep.Records, short(rep.LastHash),
			)
			return nil
		},
	}

	cmd.Flags().StringVar(&anchor, "anchor", "", "pin the genesis record's prior_hash (sha256(seed))")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the report as JSON")
	return cmd
}

func short(s string) string {
	if len(s) > 16 {
		return s[:16] + "…"
	}
	return s
}
