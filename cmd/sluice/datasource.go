// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/bino-bi/sluice/internal/config"
	pkgds "github.com/bino-bi/sluice/pkg/datasource"
)

// newDataSourceCmd groups `sluice datasource …` subcommands.
func newDataSourceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "datasource",
		Short:   "Inspect configured data sources",
		Aliases: []string{"ds"},
	}
	cmd.AddCommand(newDataSourceCheckCmd())
	return cmd
}

// dataSourceCheck is the CLI-facing shape we emit as JSON.
type dataSourceCheck struct {
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	FactoryKnown bool     `json:"factory_known"`
	Schemas      []string `json:"schemas,omitempty"`
	Tables       []string `json:"tables,omitempty"`
	Error        string   `json:"error,omitempty"`
}

func newDataSourceCheckCmd() *cobra.Command {
	var (
		dir    string
		only   string
		asJSON bool
	)

	cmd := &cobra.Command{
		Use:   "check [name]",
		Short: "Validate configured data sources",
		Long: `Check loads the DataSource manifests under --dir (or --policies-dir as
fallback) and reports, for each one:

  - whether the declared spec.type has a factory registered
  - the schema + table filters attached to the source
  - any structural failures (unknown type, missing fields)

Attaching each catalog to a live DuckDB and issuing a health probe is the
job of 'sluice serve' + the periodic health loop; 'check' is the spec-level
gate operators run in CI and on boot.

Exit codes:
  0  every data source passes
  1  I/O error loading the manifest directory
  2  at least one data source fails the check`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			if len(args) == 1 {
				only = args[0]
			}

			snap, err := config.LoadDirectory(ctx, config.LoadOptions{
				Sources: []config.SourceDir{{Path: dir}},
			})
			if err != nil {
				var verrs config.ValidationErrors
				if errors.As(err, &verrs) {
					for _, ve := range verrs {
						_, _ = fmt.Fprintln(cmd.ErrOrStderr(), ve.Error())
					}
					return &exitError{Code: 1}
				}
				return &exitError{Code: 1, Err: err}
			}

			if len(snap.DataSources) == 0 {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "no DataSource manifests found")
				return &exitError{Code: 2}
			}

			reports := make([]dataSourceCheck, 0, len(snap.DataSources))
			failed := 0
			for _, ds := range snap.DataSources {
				if only != "" && ds.Metadata.Name != only {
					continue
				}
				c := dataSourceCheck{
					Name:    ds.Metadata.Name,
					Type:    string(ds.Spec.Type),
					Schemas: ds.Spec.Schemas,
					Tables:  ds.Spec.Tables,
				}
				if _, ok := pkgds.Lookup(string(ds.Spec.Type)); ok {
					c.FactoryKnown = true
				} else {
					c.Error = fmt.Sprintf("unknown spec.type %q (registered: %v)", ds.Spec.Type, pkgds.Types())
					failed++
				}
				reports = append(reports, c)
			}

			if only != "" && len(reports) == 0 {
				return &exitError{Code: 1, Msg: fmt.Sprintf("no DataSource named %q", only)}
			}

			if asJSON {
				if err := writeJSON(cmd.OutOrStdout(), map[string]any{
					"ok":          failed == 0,
					"datasources": reports,
					"failed":      failed,
				}); err != nil {
					return err
				}
			} else {
				w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				_, _ = fmt.Fprintln(w, "NAME\tTYPE\tFACTORY\tSCHEMAS\tTABLES\tERROR")
				_, _ = fmt.Fprintln(w, "----\t----\t-------\t-------\t------\t-----")
				for _, c := range reports {
					factory := "yes"
					if !c.FactoryKnown {
						factory = "NO"
					}
					_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%s\n",
						c.Name, c.Type, factory, len(c.Schemas), len(c.Tables), c.Error,
					)
				}
				_ = w.Flush()
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\n%d checked, %d failed\n", len(reports), failed)
			}

			if failed > 0 {
				return &exitError{Code: 2}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "./policies.d", "directory containing DataSource manifests")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the report as JSON")
	return cmd
}
