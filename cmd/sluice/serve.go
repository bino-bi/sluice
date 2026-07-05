// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
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
  SIGINT / SIGTERM  begin graceful shutdown`,
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

			if deps.watcher != nil {
				deps.watcher.Start(ctx)
				// SIGHUP: block-level reload handler. Unix-only; the admin
				// /admin/reload endpoint is the cross-platform fallback.
				hupCh := make(chan os.Signal, 1)
				signal.Notify(hupCh, syscall.SIGHUP)
				go func() {
					for {
						select {
						case <-ctx.Done():
							signal.Stop(hupCh)
							return
						case <-hupCh:
							if err := deps.watcher.Reload(ctx); err != nil {
								deps.log.Warn("SIGHUP reload failed",
									"error", err.Error())
							}
						}
					}
				}()
			}

			deps.log.Info("sluice: starting",
				"rest", deps.server.REST.Listen,
				"mcp_enabled", deps.server.MCP.Enabled,
				"admin_enabled", deps.server.Admin.Enabled,
				"hot_reload", deps.watcher != nil,
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

			// Block on the first signal or the first transport error.
			err = g.Wait()
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
