// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"syscall"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/secrets"
	"github.com/bino-bi/sluice/internal/synth/build"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// synthName is the name rule of a synthetic data source. The report
// command checks it before the name becomes part of a path.
var synthName = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

// newSynthCmd groups `sluice synth …` subcommands.
func newSynthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "synth",
		Short: "Build and inspect synthetic data snapshots",
	}
	cmd.AddCommand(newSynthBuildCmd())
	cmd.AddCommand(newSynthReportCmd())
	return cmd
}

func newSynthBuildCmd() *cobra.Command {
	var (
		serverPath string
		policyDir  string
		outDir     string
		asJSON     bool
	)
	cmd := &cobra.Command{
		Use:   "build <name>",
		Short: "Build a snapshot of a synthetic data source",
		Long: `Build reads the source of the synthetic data source <name>, generates
the rows and publishes a new snapshot with its report.

Exit codes:
  0  the build succeeded
  1  I/O error, unknown name, or the name is not a synthetic data source
  3  a policy or the server config is not valid
  4  the build failed`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			parent := cmd.Context()
			if parent == nil {
				parent = context.Background()
			}
			ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			stderr := cmd.ErrOrStderr()

			scfg, err := config.LoadServer(serverPath, nil)
			if err != nil {
				return &exitError{Code: 1, Err: fmt.Errorf("server config: %w", err)}
			}
			if err := scfg.Validate(); err != nil {
				_, _ = fmt.Fprintln(stderr, err.Error())
				return &exitError{Code: 3}
			}
			log := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: parseLogLevel(scfg.Logging.Level)}))

			dir := policyDir
			if dir == "" {
				dir = scfg.Policies.Directory
			}
			snap, err := config.LoadDirectory(ctx, config.LoadOptions{Sources: []config.SourceDir{{Path: dir}}})
			if err != nil {
				var verrs config.ValidationErrors
				if errors.As(err, &verrs) {
					for _, ve := range verrs {
						_, _ = fmt.Fprintln(stderr, ve.Error())
					}
					return &exitError{Code: 3}
				}
				return &exitError{Code: 1, Err: err}
			}
			tagger, err := policy.NewColumnTagger(snap)
			if err != nil {
				_, _ = fmt.Fprintln(stderr, err.Error())
				return &exitError{Code: 3}
			}
			ds, src, err := findSynthetic(snap.DataSources, args[0])
			if err != nil {
				return &exitError{Code: 1, Err: err}
			}

			if outDir == "" {
				outDir = scfg.Synthetic.Dir
			}
			rep, err := build.Run(ctx, build.Input{
				DataSource: ds,
				Source:     src,
				Secrets:    secrets.NewDataSourceResolver(secrets.NewResolver(secrets.ResolverOptions{Logger: log})),
				Tagger:     tagger,
				Dir:        outDir,
				MaxRatio:   scfg.Synthetic.MaxRatio,
				Harden: executor.HardenConfig{
					MemoryLimit:   scfg.DuckDB.MemoryLimit,
					Threads:       scfg.DuckDB.Threads,
					TempDirectory: scfg.DuckDB.TempDir,
				},
				Origin: "cli",
				Log:    log,
				Audit: func(r *build.Report) *audit.Record {
					rec := build.AuditRecord(r, "end")
					rec.Subject = audit.Subject{ID: "cli"}
					return rec
				},
			})
			if rep == nil {
				return &exitError{Code: 1, Err: err}
			}
			// The CLI does not append to the audit log: a second writer
			// would fork the hash chain of a running server.
			if rep.Audit != nil {
				line, merr := json.Marshal(rep.Audit)
				if merr == nil {
					_, _ = fmt.Fprintln(stderr, string(line))
				}
			}
			if asJSON {
				if werr := writeJSON(cmd.OutOrStdout(), rep); werr != nil {
					return werr
				}
			} else {
				printBuildSummary(cmd.OutOrStdout(), rep)
			}
			if err != nil {
				return &exitError{Code: 4, Err: fmt.Errorf("synthetic build %s: %w", args[0], err)}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&serverPath, "config", "", "path to sluice.yaml (defaults apply when omitted)")
	cmd.Flags().StringVar(&policyDir, "policies-dir", "", "override policies.directory from sluice.yaml")
	cmd.Flags().StringVar(&outDir, "out", "", "override synthetic.dir from sluice.yaml")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the report as JSON")
	return cmd
}

func newSynthReportCmd() *cobra.Command {
	var (
		serverPath string
		outDir     string
		buildID    string
		asJSON     bool
	)
	cmd := &cobra.Command{
		Use:   "report <name>",
		Short: "Print the report of a synthetic snapshot",
		Long: `Report prints the report of the current build of <name>, or of the
build named by --build.

Exit codes:
  0  success
  1  I/O error, a name or build id that is not valid, or no report`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !synthName.MatchString(name) {
				return &exitError{Code: 1, Msg: fmt.Sprintf("%q is not a synthetic data source name", name)}
			}
			scfg, err := config.LoadServer(serverPath, nil)
			if err != nil {
				return &exitError{Code: 1, Err: fmt.Errorf("server config: %w", err)}
			}
			if outDir == "" {
				outDir = scfg.Synthetic.Dir
			}
			l := build.Layout{Dir: outDir, Name: name}
			id := buildID
			if id == "" {
				id, err = l.ReadCurrent()
				if err != nil {
					return &exitError{Code: 1, Err: err}
				}
				if id == "" {
					return &exitError{Code: 1, Msg: fmt.Sprintf("no report: %s has no current build", name)}
				}
			} else if !build.ValidBuildID(id) {
				return &exitError{Code: 1, Err: build.ErrInvalidBuildID}
			}

			if asJSON {
				data, err := os.ReadFile(l.Report(id))
				if err != nil {
					return &exitError{Code: 1, Err: noReport(id, err)}
				}
				_, err = cmd.OutOrStdout().Write(data)
				return err
			}
			rep, err := l.ReadReport(id)
			if err != nil {
				return &exitError{Code: 1, Err: noReport(id, err)}
			}
			printReport(cmd.OutOrStdout(), rep)
			return nil
		},
	}
	cmd.Flags().StringVar(&serverPath, "config", "", "path to sluice.yaml (defaults apply when omitted)")
	cmd.Flags().StringVar(&outDir, "out", "", "override synthetic.dir from sluice.yaml")
	cmd.Flags().StringVar(&buildID, "build", "", "build id to show instead of the current build")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the report as JSON")
	return cmd
}

func noReport(id string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("no report for build %s", id)
	}
	return err
}

// findSynthetic returns the synthetic data source name and the source it
// reads.
func findSynthetic(sources []*apitypes.DataSource, name string) (ds, src *apitypes.DataSource, err error) {
	byName := make(map[string]*apitypes.DataSource, len(sources))
	for _, d := range sources {
		byName[d.Metadata.Name] = d
	}
	ds = byName[name]
	switch {
	case ds == nil:
		return nil, nil, fmt.Errorf("no DataSource named %q", name)
	case ds.Spec.Type != apitypes.DSSynthetic || ds.Spec.Synthetic == nil:
		return nil, nil, fmt.Errorf("DataSource %q is not synthetic", name)
	}
	// config.LoadDirectory already checked that the source exists.
	return ds, byName[ds.Spec.Synthetic.Source], nil
}

func printBuildSummary(w io.Writer, r *build.Report) {
	_, _ = fmt.Fprintf(w, "build %s %s\n", r.BuildID, r.Result)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "TABLE\tSOURCE ROWS\tSYNTHETIC ROWS")
	for _, t := range r.Tables {
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%d\n", t.Name, t.SourceRows, t.SyntheticRows)
	}
	_ = tw.Flush()
	printBuildWarnings(w, r)
}

func printReport(w io.Writer, r *build.Report) {
	_, _ = fmt.Fprintf(w, "build %s %s, origin %s, finished %s\n", r.BuildID, r.Result, r.Origin, r.FinishedAt.Format("2006-01-02T15:04:05Z07:00"))
	if r.Error != "" {
		_, _ = fmt.Fprintf(w, "error: %s\n", r.Error)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "TABLE\tCOLUMN\tGENERATOR\tSOURCE MEDIAN\tSYNTHETIC MEDIAN\tDISTANCE\tWARNINGS")
	for _, t := range r.Tables {
		for _, c := range t.Columns {
			var srcMed, synMed *float64
			if c.Source != nil {
				srcMed = c.Source.Median
			}
			if c.Synthetic != nil {
				synMed = c.Synthetic.Median
			}
			dist := "-"
			if c.Distance != nil {
				dist = c.Distance.Metric + " " + strconv.FormatFloat(c.Distance.Value, 'g', 3, 64)
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%d\n", t.Name, c.Name, orDash(c.Generator), num(srcMed), num(synMed), dist, len(c.Warnings))
		}
	}
	_ = tw.Flush()
	printBuildWarnings(w, r)
}

func printBuildWarnings(w io.Writer, r *build.Report) {
	for _, s := range r.Spikes {
		_, _ = fmt.Fprintf(w, "spike: %s.%s, %s side, %d rows\n", s.Table, s.Column, s.Side, s.Rows)
	}
	// r.Warnings holds every column warning with its table and column.
	for _, msg := range r.Warnings {
		_, _ = fmt.Fprintf(w, "warning: %s\n", msg)
	}
}

func num(v *float64) string {
	if v == nil {
		return "-"
	}
	return strconv.FormatFloat(*v, 'g', 6, 64)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
