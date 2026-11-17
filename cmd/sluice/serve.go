// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

// newServeCmd returns the `sluice serve` subcommand — the composition root
// that assembles every internal package into a running server.
func newServeCmd() *cobra.Command {
	var (
		serverCfgPath string
		policyDir     string
	)

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the sluice server",
		Long: `Serve wires every internal package (parser, datasource registry,
executor, policy engine, rewriter, audit dispatcher, identity, and
queryservice) and starts the REST transport (always), MCP transport (when
enabled in config), and admin transport (when enabled).

Signals:
  SIGINT / SIGTERM  begin graceful shutdown
  SIGHUP            reload the policy directory`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			parent := cmd.Context()
			if parent == nil {
				parent = context.Background()
			}
			ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			deps, err := buildRuntime(ctx, serverCfgPath, policyDir)
			if err != nil {
				return &exitError{Code: 1, Err: err}
			}
			defer func() {
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				deps.Close(shutdownCtx)
			}()

			// A reload that invalidates the stdio MCP credential stops serve.
			ctx, cancel := context.WithCancelCause(ctx)
			defer cancel(nil)
			if deps.mcpPinned != nil {
				recheckPinned(deps, deps.mcpPinned, cancel)
			}
			startReloads(ctx, deps)

			deps.log.Info("sluice: starting",
				"rest", deps.server.REST.Listen,
				"mcp_enabled", deps.server.MCP.Enabled,
				"admin_enabled", deps.server.Admin.Enabled,
				"hot_reload", deps.server.Policies.Reload,
			)

			g, gctx := errgroup.WithContext(ctx)

			g.Go(func() error {
				if err := deps.rest.ListenAndServe(gctx); err != nil {
					return fmt.Errorf("rest: %w", err)
				}
				return nil
			})

			if deps.mcp != nil {
				g.Go(func() error {
					if err := deps.mcp.Run(gctx); err != nil && !errors.Is(err, context.Canceled) {
						return fmt.Errorf("mcp: %w", err)
					}
					return nil
				})
			}

			if deps.admin != nil {
				g.Go(func() error {
					if err := deps.admin.ListenAndServe(gctx); err != nil {
						return fmt.Errorf("admin: %w", err)
					}
					return nil
				})
			}

			if deps.approvals != nil {
				g.Go(func() error {
					deps.approvals.Run(gctx) // janitor; returns on ctx cancel
					return nil
				})
			}

			// build.onStart: after the listeners started, so the server
			// is ready at once and serves not_built until the swap.
			if deps.synth != nil {
				deps.synth.StartMissing()
			}

			// Block on the first signal or the first transport error.
			err = g.Wait()
			if cause := context.Cause(ctx); errors.Is(cause, errPinnedIdentity) {
				return &exitError{Code: 1, Err: cause}
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				return &exitError{Code: 2, Err: err}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&serverCfgPath, "config", "", "path to sluice.yaml (defaults apply when omitted)")
	cmd.Flags().StringVar(&policyDir, "policies-dir", "", "override policies.directory from sluice.yaml")
	return cmd
}
