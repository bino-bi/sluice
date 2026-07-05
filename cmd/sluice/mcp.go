// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/transport/mcp"
)

// newMCPCmd returns the `sluice mcp` subcommand: a Model Context Protocol
// server for local AI agents. Unlike `serve` (which exposes REST + optional
// MCP), this command runs ONLY the MCP surface and, for the stdio transport,
// authenticates once at startup from a static credential and pins that
// identity onto every tool call.
func newMCPCmd() *cobra.Command {
	var (
		serverCfgPath string
		policyDir     string
		transport     string
		jwt           string
		apiKey        string
		allowAnon     bool
	)

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run the MCP server for AI agents (stdio by default)",
		Long: `Run the Model Context Protocol server so an AI agent (e.g. Claude
Desktop) can query your data through Sluice's policy engine.

Authentication:
  Pass a long-lived credential that the agent runs as. It is verified once at
  startup through the same identity pipeline the REST/HTTP transports use, and
  the resulting identity is applied to every tool call:

    sluice mcp --config server.yaml --policies-dir policies.d \
      --jwt "$SLUICE_MCP_TOKEN"

  --jwt / SLUICE_MCP_TOKEN   a static JWT bearer token (RS/ES via JWKS, or HS
                             when an hmacSecretRef is configured on the binding)
  --api-key                  a static API key ("<id>.<secret>")

  With --transport streamable_http, credentials are taken per request from the
  HTTP Authorization / X-Api-Key header instead, and each request is
  authenticated (fail-closed) rather than pinned.

Exit codes:
  0  clean shutdown
  1  startup / auth failure`,
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

			mode := mcp.TransportMode(transport)

			// For stdio, resolve the static credential into a pinned identity
			// now. For HTTP, the auth middleware runs per request instead.
			var pinned *identity.UserCtx
			if mode != mcp.TransportStreamableHTTP {
				token := jwt
				if token == "" {
					token = os.Getenv("SLUICE_MCP_TOKEN")
				}
				switch {
				case token != "" || apiKey != "":
					u, err := resolveStaticIdentity(ctx, deps.identifier, token, apiKey)
					if err != nil {
						return &exitError{Code: 1, Err: fmt.Errorf("authenticate static credential: %w", err)}
					}
					pinned = u
					deps.log.Info("mcp: authenticated static identity", "subject", u.Subject, "issuer", u.Issuer)
				case allowAnon:
					deps.log.Warn("mcp: running anonymous — every query is default-denied unless a policy grants the anonymous subject")
				default:
					return &exitError{Code: 1, Err: errors.New("no credential: pass --jwt, --api-key, or set SLUICE_MCP_TOKEN (or --allow-anonymous to run without one)")}
				}
			}

			srv, err := mcp.New(mcp.Config{
				Enabled:        true,
				Transport:      mode,
				HTTPListen:     deps.server.MCP.Listen,
				SessionIdleMax: deps.server.MCP.SessionIdleMax,
				AllowAnonymous: allowAnon,
			}, mcp.Deps{
				Service:    deps.service,
				Identifier: deps.identifier,
				Catalogs:   registryCatalogLister{r: deps.sourceReg},
				Logger:     deps.log,
				PinnedUser: pinned,
			})
			if err != nil {
				return &exitError{Code: 1, Err: err}
			}
			if err := srv.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				return &exitError{Code: 1, Err: err}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&serverCfgPath, "config", "", "server config file (SLUICE_* env also applies)")
	cmd.Flags().StringVar(&policyDir, "policies-dir", "", "policy directory (overrides policies.directory)")
	cmd.Flags().StringVar(&transport, "transport", "stdio", "transport: stdio or streamable_http")
	cmd.Flags().StringVar(&jwt, "jwt", "", "static JWT bearer token to authenticate as (stdio)")
	cmd.Flags().StringVar(&apiKey, "api-key", "", "static API key to authenticate as (stdio)")
	cmd.Flags().BoolVar(&allowAnon, "allow-anonymous", false, "run without a credential (queries default-denied unless a policy allows the anonymous subject)")
	return cmd
}

// resolveStaticIdentity verifies a static credential through the composite
// identifier by synthesising an HTTP request that carries it, reusing the
// exact verification path the transports use.
func resolveStaticIdentity(ctx context.Context, id identity.Identifier, jwt, apiKey string) (*identity.UserCtx, error) {
	if id == nil {
		return nil, errors.New("no identifier configured (set identity.apiKeyPepper and/or SubjectBindings)")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	if err != nil {
		return nil, err
	}
	if jwt != "" {
		req.Header.Set("Authorization", "Bearer "+jwt)
	}
	if apiKey != "" {
		req.Header.Set("X-Api-Key", apiKey)
	}
	return id.Identify(ctx, req)
}
