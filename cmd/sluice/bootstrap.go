// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/bino-bi/sluice/internal/approval"
	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/budget"
	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/datasource"
	_ "github.com/bino-bi/sluice/internal/datasource/drivers/duckdbfile" // driver registration
	_ "github.com/bino-bi/sluice/internal/datasource/drivers/motherduck" // driver registration
	_ "github.com/bino-bi/sluice/internal/datasource/drivers/mysql"      // driver registration
	_ "github.com/bino-bi/sluice/internal/datasource/drivers/postgres"   // driver registration
	_ "github.com/bino-bi/sluice/internal/datasource/drivers/s3parquet"  // driver registration
	_ "github.com/bino-bi/sluice/internal/datasource/drivers/sqlitefile" // driver registration
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/parserbackend"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/policycache"
	"github.com/bino-bi/sluice/internal/queryservice"
	"github.com/bino-bi/sluice/internal/ratelimit"
	"github.com/bino-bi/sluice/internal/rewriter"
	"github.com/bino-bi/sluice/internal/schema"
	"github.com/bino-bi/sluice/internal/secrets"
	"github.com/bino-bi/sluice/internal/telemetry"
	"github.com/bino-bi/sluice/internal/transport/admin"
	"github.com/bino-bi/sluice/internal/transport/mcp"
	"github.com/bino-bi/sluice/internal/transport/rest"
	"github.com/bino-bi/sluice/internal/version"

	"github.com/bino-bi/sluice/pkg/apitypes"
)

// runtimeDeps is the fully-wired dependency graph the `serve` subcommand
// starts and shuts down. Each field is owned by the bootstrap and freed by
// the returned Close function (reverse order of construction).
type runtimeDeps struct {
	log          *slog.Logger
	server       *config.ServerConfig
	snapshot     *config.Snapshot
	registry     *config.Registry
	resolver     *secrets.Resolver
	parser       parser.Parser
	sourceReg    *datasource.Registry
	exec         *executor.Executor
	schemaCache  schema.Cache
	policyEng    *policy.Engine      // YAML engine; also the admin snapshot source
	policyEngine policy.PolicyEngine // engine passed to queryservice (yaml or composite)
	rewrite      *rewriter.Rewriter
	auditDisp    *audit.Dispatcher
	auditSinks   []audit.Sink
	identifier   identity.Identifier
	apikey       *identity.APIKeyIdentifier
	jwtID        *identity.JWTIdentifier
	jwtBindings  *identity.BindingRegistry
	service      *queryservice.Service
	rateLimiter  *ratelimit.Limiter
	rewriteCache *policycache.Cache
	approvals    *approval.Broker
	budget       *budget.Manager
	rest         *rest.Server
	mcp          *mcp.Server
	admin        *admin.Server
	watcher      *config.Watcher
	telShutdown  func(context.Context) error
}

// buildRuntime wires every layer in the correct order. The returned deps
// are ready to be started; invoking Close cleans up in reverse. Errors are
// wrapped with a context-specific prefix so the caller can exit with the
// right code.
func buildRuntime(ctx context.Context, serverCfgPath, policyDir string) (*runtimeDeps, error) {
	deps := &runtimeDeps{}

	// 1. Server config. Validate refuses settings this build cannot
	// enforce — an operator must never believe a control is active when
	// it is not.
	scfg, err := config.LoadServer(serverCfgPath, nil)
	if err != nil {
		return nil, fmt.Errorf("load server config: %w", err)
	}
	if err := scfg.Validate(); err != nil {
		return nil, fmt.Errorf("server config: %w", err)
	}
	deps.server = scfg

	// 2. Telemetry (slog + optional prom gauge).
	telCfg := telemetry.DefaultConfig(telemetry.ServiceInfo{
		Name:    "sluice",
		Version: version.Current().Version,
		Commit:  version.Current().Commit,
	})
	telCfg.Logging.Level = parseLogLevel(scfg.Logging.Level)
	telCfg.Logging.Format = strings.ToLower(scfg.Logging.Format)
	telCfg.Metrics.Enabled = scfg.Admin.Enabled
	shutdown, err := telemetry.Init(ctx, telCfg)
	if err != nil {
		return nil, fmt.Errorf("telemetry init: %w", err)
	}
	deps.telShutdown = shutdown
	deps.log = slog.Default()

	// 3. Secrets resolver (env + file providers).
	deps.resolver = secrets.NewResolver(secrets.ResolverOptions{Logger: deps.log})

	// 4. Snapshot (policies + data sources + subject bindings).
	dir := policyDir
	if dir == "" {
		dir = scfg.Policies.Directory
	}
	snap, err := config.LoadDirectory(ctx, config.LoadOptions{
		Sources: []config.SourceDir{{Path: dir}},
	})
	if err != nil {
		return nil, fmt.Errorf("load policy directory %q: %w", dir, err)
	}
	deps.snapshot = snap

	// 5. Registry — atomic snapshot holder for downstream subscribers.
	deps.registry = config.NewRegistry()
	deps.registry.Publish(snap)

	// 6. Parser.
	deps.parser = parserbackend.New(parser.Options{
		MaxSQLBytes: scfg.Limits.MaxSQLBytes,
		Logger:      deps.log,
	})
	version.SetPgQueryVersion(parserbackend.Version())

	// 7. Data-source registry (AttachHook only usable after executor init).
	deps.sourceReg, err = datasource.New(ctx, datasource.Options{
		Snapshot: &datasource.Snapshot{DataSources: snap.DataSources},
		Secrets:  secrets.NewDataSourceResolver(deps.resolver),
		Logger:   deps.log,
		FailFast: scfg.DataSources.FailFast,
	})
	if err != nil {
		return nil, fmt.Errorf("datasource registry: %w", err)
	}

	// 8. Executor (DuckDB); installs AttachHook so every fresh connection
	//    re-ATTACHes every catalog.
	deps.exec, err = executor.New(ctx, executor.Config{
		Path: "",
		Harden: executor.HardenConfig{
			MemoryLimit:   scfg.DuckDB.MemoryLimit,
			Threads:       scfg.DuckDB.Threads,
			TempDirectory: scfg.DuckDB.TempDir,
		},
		AttachHook:              deps.sourceReg.AttachHook(),
		MaxOpen:                 scfg.DuckDB.MaxOpen,
		MaxIdle:                 scfg.DuckDB.MaxIdle,
		ConnMaxIdle:             scfg.DuckDB.ConnMaxIdle,
		DefaultStatementTimeout: scfg.Limits.QueryTimeout,
		Logger:                  deps.log,
	})
	if err != nil {
		return nil, fmt.Errorf("executor: %w", err)
	}

	// 8b. Health sweep pool — same *sql.DB the schema cache borrows from.
	// Kicks one immediate sweep so /v1/ready reflects real state shortly
	// after boot instead of after the first full HealthInterval.
	deps.sourceReg.SetPool(ctx, datasource.NewSQLPool(deps.exec.DB()))

	// 9. Schema cache — wired to the pool via a narrow ConnProvider.
	deps.schemaCache = schema.New(schema.Options{
		Loader: schema.NewLoader(deps.sourceReg, deps.exec.DB()),
		Logger: deps.log,
	})

	// 10. Policy engine(s) + snapshot apply. The YAML engine always exists
	// (admin snapshot introspection + composite member); the engine passed
	// to the queryservice may be a composite.
	deps.policyEng, deps.policyEngine, err = buildPolicyEngine(scfg, deps.resolver, deps.log)
	if err != nil {
		return nil, fmt.Errorf("policy engine: %w", err)
	}
	if err := deps.policyEngine.ApplySnapshot(ctx, snap); err != nil {
		return nil, fmt.Errorf("policy snapshot: %w", err)
	}

	// 11. Rewriter.
	deps.rewrite = rewriter.New(rewriter.Options{
		Parser: deps.parser,
		Schema: deps.schemaCache,
		Logger: deps.log,
		Salts:  secrets.NewSaltStore(deps.resolver),
	})

	// 12. Audit dispatcher — file sink (chain primary) plus optional
	// best-effort syslog / s3 secondaries.
	deps.auditSinks, err = buildAuditSinks(ctx, scfg, deps.resolver, deps.log)
	if err != nil {
		return nil, err
	}

	genesis, err := resolveGenesisSeed(ctx, deps.resolver, scfg.Audit.File)
	if err != nil {
		return nil, fmt.Errorf("audit genesis: %w", err)
	}
	host, _ := os.Hostname()
	deps.auditDisp, err = audit.NewDispatcher(audit.DispatcherOptions{
		Sinks:       deps.auditSinks,
		GenesisSeed: genesis,
		Origin:      host,
		Logger:      deps.log,
	})
	if err != nil {
		return nil, fmt.Errorf("audit dispatcher: %w", err)
	}

	// 13. Identity — JWT via SubjectBindings + optional API-key pepper.
	ids, err := buildIdentity(ctx, scfg, deps.resolver, snap, deps.log)
	if err != nil {
		return nil, fmt.Errorf("identity: %w", err)
	}
	deps.identifier = ids.identifier
	deps.apikey = ids.apikey
	deps.jwtID = ids.jwt
	deps.jwtBindings = ids.bindings

	// 14. Per-subject rate limiter (from SubjectBinding.spec.rateLimit,
	// falling back to limits.defaultSubjectRps for bindings without one).
	deps.rateLimiter = ratelimit.New(nil)
	deps.rateLimiter.SetDefault(ratelimit.Spec{
		RPS:   scfg.Limits.DefaultSubjectRPS,
		Burst: scfg.Limits.DefaultSubjectBurst,
	})
	{
		bySub, byIss := buildRateSpecs(snap)
		deps.rateLimiter.SetSpecs(bySub, byIss)
	}

	// 14b. Rewrite cache (opt-in). Built before the queryservice so the
	// reload subscriber can Purge it on every snapshot publish.
	if scfg.Cache.Rewrite.Enabled {
		deps.rewriteCache = policycache.New(scfg.Cache.Rewrite.Size, scfg.Cache.Rewrite.TTL)
	}

	// 14c. Approval broker (built before the queryservice so it can share
	// the instance). Active when at least one ApprovalPolicy is loaded or a
	// public base URL is configured; policies without a base URL fail
	// startup fail-closed. The sync wait is clamped to fit the REST request
	// timeout so a hybrid wait returns ERR_APPROVAL_PENDING rather than
	// being killed by the timeout middleware.
	approvalSyncClamp(scfg, deps.log)
	deps.approvals, err = buildApprovalBroker(ctx, scfg, snap, deps.resolver, deps.auditDisp, deps.log)
	if err != nil {
		return nil, fmt.Errorf("approval broker: %w", err)
	}

	// 14d. Per-subject budget manager (opt-in, SQLite-backed).
	deps.budget, err = buildBudgetManager(scfg, snap, deps.log)
	if err != nil {
		return nil, fmt.Errorf("budget manager: %w", err)
	}

	qopts := queryservice.Options{
		Parser:          deps.parser,
		Policy:          deps.policyEngine,
		Rewriter:        deps.rewrite,
		Executor:        deps.exec,
		Audit:           deps.auditDisp,
		Schema:          deps.schemaCache,
		Logger:          deps.log,
		RateLimiter:     deps.rateLimiter,
		AuditBestEffort: !scfg.Audit.FailClosed,
		Keys:            secrets.NewKeyStore(deps.resolver),
		Salts:           secrets.NewSaltStore(deps.resolver),
		Limits: queryservice.Limits{
			DefaultMaxRows:         scfg.Limits.MaxRows,
			MaxRowsCeiling:         scfg.Limits.MaxRowsCeiling,
			DefaultTimeout:         scfg.Limits.QueryTimeout,
			MaxTimeout:             scfg.Limits.MaxQueryTimeout,
			MaxSQLBytes:            scfg.Limits.MaxSQLBytes,
			MaxConcurrent:          scfg.Limits.MaxConcurrent,
			DisableCrossCatalog:    scfg.Limits.DisableCrossCatalog,
			SQLSampleBytes:         scfg.Audit.SQLSampleBytes,
			ApprovalSQLSampleBytes: scfg.Approval.SQLSampleBytes,
		},
	}
	if deps.rewriteCache != nil {
		qopts.Cache = deps.rewriteCache
	}
	if deps.approvals != nil {
		qopts.Approvals = deps.approvals
		qopts.ApprovalSyncWait = scfg.Approval.SyncWait
	}
	if deps.budget != nil {
		qopts.Budget = deps.budget
	}
	deps.service = queryservice.New(qopts)

	// 15. Transports.
	restCfg := rest.Config{
		Listen:         scfg.REST.Listen,
		MaxBodyBytes:   scfg.REST.MaxBodyBytes,
		RequestTimeout: scfg.REST.RequestTimeout,
	}
	if t := scfg.REST.TLS; t != nil && t.CertFile != "" && t.KeyFile != "" {
		restCfg.TLS = &rest.TLSConfig{CertFile: t.CertFile, KeyFile: t.KeyFile}
	}
	restDeps := rest.Deps{
		Service:    deps.service,
		Identifier: deps.identifier,
		Registry:   deps.sourceReg,
		Approvals:  approvalGateway(deps.approvals),
		Logger:     deps.log,
	}
	// Transport-level buckets sit ahead of identity on /v1/query; zero RPS
	// disables them (limits.* is restart-only, like the other limits).
	if scfg.Limits.GlobalRPS > 0 {
		restDeps.GlobalLimiter = ratelimit.NewKeyed(
			ratelimit.Spec{RPS: scfg.Limits.GlobalRPS, Burst: scfg.Limits.GlobalBurst}, 1, nil)
	}
	if scfg.Limits.PerIPRPS > 0 {
		restDeps.PerIPLimiter = ratelimit.NewKeyed(
			ratelimit.Spec{RPS: scfg.Limits.PerIPRPS, Burst: scfg.Limits.PerIPBurst},
			scfg.Limits.PerIPMaxBuckets, nil)
	}
	deps.rest = rest.New(restCfg, restDeps)

	if scfg.MCP.Enabled {
		pinned, err := resolveMCPPinnedUser(ctx, scfg, deps.resolver, deps.identifier, deps.log)
		if err != nil {
			return nil, fmt.Errorf("mcp: %w", err)
		}
		deps.mcp, err = mcp.New(mcp.Config{
			Enabled:        true,
			Transport:      mcp.TransportMode(scfg.MCP.Transport),
			HTTPListen:     scfg.MCP.Listen,
			SessionIdleMax: scfg.MCP.SessionIdleMax,
			AllowAnonymous: scfg.MCP.AllowAnonymous,
		}, mcp.Deps{
			Service:    deps.service,
			Identifier: deps.identifier,
			Catalogs:   registryCatalogLister{r: deps.sourceReg},
			Logger:     deps.log,
			PinnedUser: pinned,
		})
		if err != nil {
			return nil, fmt.Errorf("mcp: %w", err)
		}
	}

	// 16. Config watcher — fsnotify + SIGHUP + admin /reload all funnel
	//     through here, republishing the snapshot to subscribers on change.
	if scfg.Policies.Reload {
		deps.watcher, err = config.NewWatcher(config.WatchOptions{
			Dir:      dir,
			Registry: deps.registry,
			Logger:   deps.log,
			// Dry-run compile before publish: a snapshot the policy
			// engine would reject must not reach the other subscribers
			// (bindings, limits, budgets) or the gateway ends up
			// half-applied.
			Validate: func(ctx context.Context, snap *config.Snapshot) error {
				_, err := policy.Compile(ctx, snap)
				return err
			},
		})
		if err != nil {
			return nil, fmt.Errorf("config watcher: %w", err)
		}
		deps.registry.Subscribe(func(_ *config.Snapshot, cur *config.Snapshot) {
			if cur == nil {
				return
			}
			deps.applyReload(ctx, cur)
		})
	}

	if scfg.Admin.Enabled {
		deps.admin = admin.New(admin.Config{
			Enabled: true,
			Listen:  scfg.Admin.Listen,
			Token:   scfg.Admin.Token,
		}, admin.Deps{
			Service:   deps.service,
			Approvals: adminPendingLister(deps.approvals),
			Policies:  deps.policyEng,
			Sources:   deps.sourceReg,
			Catalogs:  registryCatalogLister{r: deps.sourceReg},
			Logger:    deps.log,
			Reloader:  reloaderFromWatcher(deps.watcher),
		})
	}

	return deps, nil
}

// applyReload applies a published snapshot to every hot-swappable
// component. Never fatal: each failure logs and keeps the previous state,
// so a bad reload cannot take the gateway down. Nil guards on every dep
// keep the method usable from minimal test fixtures.
func (d *runtimeDeps) applyReload(ctx context.Context, cur *config.Snapshot) {
	if d.resolver != nil {
		// Invalidate cached secrets so rotated hashRef / hmacSecretRef
		// URIs return the new value on resolve.
		d.resolver.Invalidate()
	}
	if d.policyEngine != nil {
		if err := d.policyEngine.ApplySnapshot(ctx, cur); err != nil {
			d.log.Warn("reload: policy engine rejected snapshot",
				slog.String("error", err.Error()))
		}
	}
	if d.apikey != nil {
		bindings, err := buildAPIKeyBindings(ctx, d.resolver, cur, d.log)
		if err != nil {
			d.log.Warn("reload: api-key bindings build failed",
				slog.String("error", err.Error()))
		} else {
			d.apikey.SetBindings(bindings)
		}
	}
	if d.jwtID != nil && d.jwtBindings != nil {
		// Secrets resolve before Apply so a resolution failure never
		// half-applies; the swap order leaves at most a brief window of
		// new bindings with old secrets, never the reverse.
		hmac, err := buildHMACSecrets(ctx, d.resolver, cur)
		if err != nil {
			d.log.Warn("reload: hmac secret resolve failed; keeping previous jwt bindings",
				slog.String("error", err.Error()))
		} else if err := d.jwtBindings.Apply(derefBindings(cur.SubjectBindings)); err != nil {
			d.log.Warn("reload: jwt bindings rejected; keeping previous",
				slog.String("error", err.Error()))
		} else {
			d.jwtID.SetHMACSecrets(hmac)
		}
	}
	if d.rateLimiter != nil {
		bySub, byIss := buildRateSpecs(cur)
		d.rateLimiter.SetSpecs(bySub, byIss)
	}
	if d.budget != nil {
		d.budget.SetSpecs(buildBudgetSpecs(cur))
	}
	if d.rewriteCache != nil {
		d.rewriteCache.Purge()
	}
	// ApprovalPolicies added on reload without a configured public base
	// URL cannot be honoured (the broker was not built). Fail-closed:
	// queries will pend and expire; log loudly.
	if d.approvals == nil && len(cur.ByKind[apitypes.KindApprovalPolicy]) > 0 {
		d.log.Error("reload added ApprovalPolicies but no approval broker is configured; " +
			"set approval.publicBaseUrl and restart — matching queries will pend and expire")
	}
	if d.schemaCache != nil {
		d.schemaCache.InvalidateAll()
	}
}

// reloaderFromWatcher adapts the config.Watcher.Reload signature to the
// admin.Reloader interface. Nil is returned when the watcher is disabled
// so /admin/reload responds 501 rather than silently succeeding.
func reloaderFromWatcher(w *config.Watcher) admin.Reloader {
	if w == nil {
		return nil
	}
	return reloaderFunc(w.Reload)
}

type reloaderFunc func(ctx context.Context) error

// Reload implements admin.Reloader.
func (f reloaderFunc) Reload(ctx context.Context) error { return f(ctx) }

// Close releases every resource in reverse construction order. Safe to
// call on a partial graph — missing components are skipped.
func (d *runtimeDeps) Close(ctx context.Context) {
	if d == nil {
		return
	}
	if d.watcher != nil {
		_ = d.watcher.Close()
	}
	if d.budget != nil {
		// Final flush so the last window of usage is persisted.
		_ = d.budget.Close(ctx)
	}
	if d.approvals != nil {
		// Before the dispatcher: the broker's auditor writes through it.
		_ = d.approvals.Close(ctx)
	}
	if d.auditDisp != nil {
		_ = d.auditDisp.Close(ctx)
	}
	for _, s := range d.auditSinks {
		_ = s.Close(ctx)
	}
	if d.sourceReg != nil {
		_ = d.sourceReg.Close()
	}
	if d.exec != nil {
		_ = d.exec.Close()
	}
	if d.telShutdown != nil {
		_ = d.telShutdown(ctx)
	}
}

// registryCatalogLister bridges datasource.Registry to
// queryservice.CatalogLister without pulling that dependency into the
// datasource package itself.
type registryCatalogLister struct {
	r *datasource.Registry
}

// List implements queryservice.CatalogLister.
func (l registryCatalogLister) List(_ context.Context) []queryservice.CatalogInfo {
	if l.r == nil {
		return nil
	}
	statuses := l.r.Statuses()
	out := make([]queryservice.CatalogInfo, 0, len(statuses))
	for _, s := range statuses {
		out = append(out, queryservice.CatalogInfo{
			Name:    s.Name,
			Type:    s.Type,
			Healthy: s.Healthy,
		})
	}
	return out
}

// resolveGenesisSeed looks up scfg.Audit.File.Genesis via the secrets
// resolver when provided, otherwise returns a process-derived fallback
// (hostname + build commit). The fallback is only safe for
// single-installation use; operators are expected to configure a stable
// secret for multi-instance fleets.
func resolveGenesisSeed(ctx context.Context, r *secrets.Resolver, file *config.FileSinkConfig) ([]byte, error) {
	if file != nil && file.Genesis != "" {
		return r.Resolve(ctx, file.Genesis)
	}
	host, _ := os.Hostname()
	return []byte("sluice-genesis:" + host + ":" + version.Current().CommitFull), nil
}

// buildIdentity assembles the identity.Composite from server + policy
// snapshot. JWT lights up when at least one SubjectBinding is present.
// API-key lights up when scfg.Identity.APIKeyPepper is set; the bindings
// come from SubjectBinding.Spec.APIKeys in the snapshot. The returned
// *APIKeyIdentifier is non-nil only when the API-key branch is active,
// so the caller can hand it to the registry subscription for reload.
// identityStack bundles the composite identifier with the handles the
// reload subscriber needs to hot-swap JWT bindings, HMAC secrets, and
// API-key bindings.
type identityStack struct {
	identifier identity.Identifier
	apikey     *identity.APIKeyIdentifier
	jwt        *identity.JWTIdentifier
	bindings   *identity.BindingRegistry
}

func buildIdentity(ctx context.Context, scfg *config.ServerConfig, r *secrets.Resolver, snap *config.Snapshot, log *slog.Logger) (identityStack, error) {
	var stack identityStack

	// The JWT identifier is built even with zero SubjectBindings so a
	// reload can introduce the first binding without a restart. An empty
	// registry rejects every Bearer token with unknown-issuer (fail-closed)
	// and requests without one fall through to the API-key child.
	bindReg, err := identity.NewBindingRegistry(derefBindings(snap.SubjectBindings))
	if err != nil {
		return identityStack{}, fmt.Errorf("binding registry: %w", err)
	}
	hmacSecrets, err := buildHMACSecrets(ctx, r, snap)
	if err != nil {
		return identityStack{}, err
	}
	jwtID, err := identity.NewJWTIdentifier(identity.JWTOptions{
		Bindings:    bindReg,
		HMACSecrets: hmacSecrets,
		Logger:      log,
	})
	if err != nil {
		return identityStack{}, fmt.Errorf("jwt identifier: %w", err)
	}
	stack.jwt = jwtID
	stack.bindings = bindReg
	children := []identity.Identifier{jwtID}

	if scfg.Identity.APIKeyPepper != "" {
		pepper, err := r.Resolve(ctx, scfg.Identity.APIKeyPepper)
		if err != nil {
			return identityStack{}, fmt.Errorf("resolve apiKeyPepper: %w", err)
		}
		bindings, err := buildAPIKeyBindings(ctx, r, snap, log)
		if err != nil {
			return identityStack{}, fmt.Errorf("api-key bindings: %w", err)
		}
		stack.apikey = identity.NewAPIKeyIdentifier(identity.APIKeyOptions{
			Pepper:   pepper,
			Bindings: bindings,
			Logger:   log,
		})
		children = append(children, stack.apikey)
	}

	if len(snap.SubjectBindings) == 0 && scfg.Identity.APIKeyPepper == "" {
		// The policy engine still applies default-deny to anonymous.
		log.Warn("identity: no credentials configured — requests are anonymous until a reload adds SubjectBindings")
	}
	stack.identifier = identity.NewComposite(children...)
	return stack, nil
}

// buildRateSpecs extracts per-subject and per-issuer rate-limit specs from
// every SubjectBinding.spec.rateLimit. API-key bindings key by their
// claims.subjectId; JWT bindings key by issuer so the limit applies per
// authenticated subject under that issuer.
func buildRateSpecs(snap *config.Snapshot) (bySubject, byIssuer map[string]ratelimit.Spec) {
	bySubject = map[string]ratelimit.Spec{}
	byIssuer = map[string]ratelimit.Spec{}
	if snap == nil {
		return bySubject, byIssuer
	}
	for _, sb := range snap.SubjectBindings {
		if sb == nil || sb.Spec.RateLimit == nil || sb.Spec.RateLimit.RPS <= 0 {
			continue
		}
		spec := ratelimit.Spec{RPS: sb.Spec.RateLimit.RPS, Burst: sb.Spec.RateLimit.Burst}
		if spec.Burst <= 0 {
			spec.Burst = 1
		}
		if sb.Spec.Claims.SubjectID != "" {
			bySubject[sb.Spec.Claims.SubjectID] = spec
		}
		if sb.Spec.Issuer != "" {
			byIssuer[sb.Spec.Issuer] = spec
		}
	}
	return bySubject, byIssuer
}

// buildHMACSecrets resolves each SubjectBinding's hmacSecretRef into the
// issuer→secret map the JWT identifier uses to verify HS256/HS384 tokens.
// Without this a binding that advertises an HS* issuer could never
// authenticate, since RS/ES keys come from JWKS and HMAC has no network
// path. A trailing newline (common in file-mounted secrets) is trimmed.
func buildHMACSecrets(ctx context.Context, r *secrets.Resolver, snap *config.Snapshot) (map[string][]byte, error) {
	if snap == nil {
		return nil, nil
	}
	out := map[string][]byte{}
	for _, sb := range snap.SubjectBindings {
		if sb == nil || sb.Spec.HMACSecretRef == "" {
			continue
		}
		issuer := sb.Spec.Issuer
		if issuer == "" {
			issuer = sb.Metadata.Name
		}
		sec, err := r.Resolve(ctx, sb.Spec.HMACSecretRef)
		if err != nil {
			return nil, fmt.Errorf("resolve hmacSecretRef for issuer %q: %w", issuer, err)
		}
		out[issuer] = []byte(strings.TrimRight(string(sec), "\r\n"))
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// buildAPIKeyBindings walks every SubjectBinding in snap, resolves each
// apiKeys[].hashRef through the secrets resolver, and produces the flat
// identity.APIKeyBinding slice the APIKeyIdentifier indexes on. Per-key
// resolution failures log a warning and skip the key rather than
// aborting the whole reload — one rotated-out key should never blank the
// table.
func buildAPIKeyBindings(ctx context.Context, r *secrets.Resolver, snap *config.Snapshot, log *slog.Logger) ([]identity.APIKeyBinding, error) {
	if snap == nil {
		return nil, nil
	}
	var out []identity.APIKeyBinding
	for _, sb := range snap.SubjectBindings {
		if sb == nil || len(sb.Spec.APIKeys) == 0 {
			continue
		}
		issuer := sb.Spec.Issuer
		if issuer == "" {
			issuer = sb.Metadata.Name
		}
		claims := apiKeyLiteralClaims(sb.Spec.Claims)
		for _, k := range sb.Spec.APIKeys {
			if k.ID == "" || k.HashRef == "" {
				log.Warn("api-key binding: skipping entry with empty id or hashRef",
					slog.String("binding", sb.Metadata.Name))
				continue
			}
			hexHash, err := r.ResolveString(ctx, k.HashRef)
			if err != nil {
				log.Warn("api-key binding: hashRef resolve failed",
					slog.String("binding", sb.Metadata.Name),
					slog.String("id", k.ID),
					slog.String("error", err.Error()))
				continue
			}
			hash, err := identity.DecodeHash(hexHash)
			if err != nil {
				log.Warn("api-key binding: hashRef decode failed",
					slog.String("binding", sb.Metadata.Name),
					slog.String("id", k.ID),
					slog.String("error", err.Error()))
				continue
			}
			groups := k.Groups
			if len(groups) == 0 && sb.Spec.Claims.Groups != "" {
				groups = []string{sb.Spec.Claims.Groups}
			}
			out = append(out, identity.APIKeyBinding{
				ID:      k.ID,
				Hash:    hash,
				Subject: sb.Spec.Claims.SubjectID,
				Issuer:  issuer,
				Email:   sb.Spec.Claims.Email,
				Groups:  append([]string(nil), groups...),
				Claims:  claims,
			})
		}
	}
	return out, nil
}

// apiKeyLiteralClaims turns the ClaimPaths block into a UserCtx.Claims
// map of literal values for the API-key flow. JWT callers treat these
// fields as paths; in API-key mode they are the values to stamp onto the
// authenticated session so row-filter templates like `{{ subject.tenantId }}`
// resolve without a JWT round-trip.
func apiKeyLiteralClaims(c apitypes.ClaimPaths) map[string]any {
	out := make(map[string]any)
	if c.TenantID != "" {
		out["tenantId"] = c.TenantID
	}
	if c.AllowedRegions != "" {
		out["allowedRegions"] = c.AllowedRegions
	}
	for k, v := range c.Extra {
		if k == "" || v == "" {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// derefBindings converts []*apitypes.SubjectBinding into the
// []apitypes.SubjectBinding that identity.NewBindingRegistry expects.
func derefBindings(src []*apitypes.SubjectBinding) []apitypes.SubjectBinding {
	out := make([]apitypes.SubjectBinding, 0, len(src))
	for _, b := range src {
		if b == nil {
			continue
		}
		out = append(out, *b)
	}
	return out
}

// parseLogLevel maps the textual log level from config to slog.Level.
func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
