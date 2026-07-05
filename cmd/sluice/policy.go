// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/policy"
)

// newPolicyCmd groups `sluice policy …` subcommands.
func newPolicyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "policy",
		Short: "Inspect, validate, and explain policies",
	}
	cmd.AddCommand(newPolicyValidateCmd())
	cmd.AddCommand(newPolicyExplainCmd())
	return cmd
}

// newPolicyValidateCmd mirrors `sluice config validate <dir>` so operators
// can invoke it under the command family that matches the plan (`policy …`).
func newPolicyValidateCmd() *cobra.Command {
	var strict bool

	cmd := &cobra.Command{
		Use:   "validate <policy-dir>",
		Short: "Validate every policy manifest under <policy-dir>",
		Long: `Validate loads every *.yaml / *.yml file under <policy-dir> via the
same pipeline the server uses and reports all decoding or structural errors.

Exit codes mirror 'sluice config validate':
  0  success
  1  I/O error
  3  validation failure`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			// Structural decode passed; also run the policy compiler so a
			// manifest that is schema-valid but uses an unimplemented feature
			// (e.g. a partial/hash mask, a CEL condition, an enforcementMode
			// other than Enforce) fails here at validate time — with a precise
			// kind/name/field message — rather than aborting a live reload.
			if _, cerr := policy.Compile(context.Background(), snap); cerr != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), cerr.Error())
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "1 policy compile error(s)")
				return &exitError{Code: 3}
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(),
				"policies OK: %s (%d objects, digest %s)\n",
				args[0], len(snap.Policies), shortDigest(snap.Digest),
			)
			return nil
		},
	}

	cmd.Flags().BoolVar(&strict, "strict", false, "reject unknown YAML fields")
	return cmd
}

func newPolicyExplainCmd() *cobra.Command {
	var (
		policyDir string
		user      string
		issuer    string
		email     string
		groups    []string
		claims    []string
		tableArg  string
		sqlArg    string
		asJSON    bool
	)

	cmd := &cobra.Command{
		Use:   "explain",
		Short: "Explain the effective policy for a subject and target",
		Long: `Explain loads the policy snapshot from --policies-dir, builds a
synthetic UserCtx from the --user / --issuer / --email / --groups / --claims
flags, and reports which policies match the requested --table (dotted
catalog.schema.table).

Claims are parsed as key=value pairs. Values are treated as strings; use JSON
if you need richer types.

Exit codes:
  0  explanation rendered
  1  bad flags / snapshot load failure
  3  compile failure`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if user == "" {
				return &exitError{Code: 1, Msg: "--user is required"}
			}
			if tableArg == "" && sqlArg == "" {
				return &exitError{Code: 1, Msg: "one of --table or --sql is required"}
			}

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			snap, err := config.LoadDirectory(ctx, config.LoadOptions{
				Sources: []config.SourceDir{{Path: policyDir}},
			})
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

			engine := policy.New(policy.Options{})
			if err := engine.ApplySnapshot(ctx, snap); err != nil {
				return &exitError{Code: 3, Err: fmt.Errorf("compile snapshot: %w", err)}
			}

			uctx, err := buildUserCtx(user, issuer, email, groups, claims)
			if err != nil {
				return &exitError{Code: 1, Err: err}
			}

			var tables []parser.TableRef
			if tableArg != "" {
				ref, terr := parseTableRef(tableArg)
				if terr != nil {
					return &exitError{Code: 1, Err: terr}
				}
				tables = append(tables, ref)
			}

			result, err := engine.Explain(ctx, policy.Input{
				User:   uctx,
				Tables: tables,
				Now:    time.Now(),
			})
			if err != nil {
				return &exitError{Code: 1, Err: err}
			}

			if asJSON {
				return writeJSON(cmd.OutOrStdout(), result)
			}

			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "subject : %s\n", result.Subject)
			_, _ = fmt.Fprintf(out, "resource: %s\n", result.Resource)
			_, _ = fmt.Fprintf(out, "decision: %s\n", result.Effective.Decision)

			if len(result.Matched) == 0 && len(result.Rejected) == 0 {
				_, _ = fmt.Fprintln(out, "(no policies matched — default-deny applies)")
				return nil
			}

			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "Kind\tName\tPriority\tEffect")
			_, _ = fmt.Fprintln(w, "----\t----\t--------\t------")
			for _, p := range result.Matched {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%d\tapplied\n", p.Kind, p.Name, p.Priority)
			}
			for _, p := range result.Rejected {
				_, _ = fmt.Fprintf(w, "%s\t%s\t-\trejected: %s\n", p.Kind, p.Name, p.Reason)
			}
			if len(result.Effective.RowFilters) > 0 {
				_, _ = fmt.Fprintf(w, "row filters:\t%s\n", strings.Join(result.Effective.RowFilters, ", "))
			}
			for _, m := range result.Effective.ColumnMasks {
				_, _ = fmt.Fprintf(w, "column mask:\t%s\t-\t%s (via %s)\n", m.Column, m.MaskType, m.Policy)
			}
			return w.Flush()
		},
	}

	cmd.Flags().StringVar(&policyDir, "policies-dir", "./policies.d", "directory containing policy manifests")
	cmd.Flags().StringVar(&user, "user", "", "synthetic subject identifier (required)")
	cmd.Flags().StringVar(&issuer, "issuer", "", "synthetic issuer (iss claim)")
	cmd.Flags().StringVar(&email, "email", "", "synthetic email claim")
	cmd.Flags().StringSliceVar(&groups, "groups", nil, "subject groups (comma-separated)")
	cmd.Flags().StringSliceVar(&claims, "claims", nil, "extra claims as key=value (comma-separated)")
	cmd.Flags().StringVar(&tableArg, "table", "", "target table as catalog.schema.table")
	cmd.Flags().StringVar(&sqlArg, "sql", "", "simulated SQL (reserved; not wired until the parser is plumbed here)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit ExplainResult as JSON")

	_ = cmd.MarkFlagRequired("user")

	return cmd
}

func buildUserCtx(subject, issuer, email string, groups, claims []string) (*identity.UserCtx, error) {
	ctx := &identity.UserCtx{
		Subject:    subject,
		Issuer:     issuer,
		Email:      email,
		Groups:     groups,
		AuthMethod: identity.AuthMethodJWT,
	}
	if len(claims) > 0 {
		ctx.Claims = make(map[string]any, len(claims))
		for _, raw := range claims {
			k, v, ok := strings.Cut(raw, "=")
			if !ok {
				return nil, fmt.Errorf("claim %q: expected key=value", raw)
			}
			ctx.Claims[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return ctx, nil
}

func parseTableRef(s string) (parser.TableRef, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return parser.TableRef{}, fmt.Errorf("table %q: expected catalog.schema.table", s)
	}
	for _, p := range parts {
		if p == "" {
			return parser.TableRef{}, fmt.Errorf("table %q: segments must be non-empty", s)
		}
	}
	return parser.TableRef{Catalog: parts[0], Schema: parts[1], Table: parts[2]}, nil
}
