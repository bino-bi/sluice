// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/bino-bi/sluice/internal/approval"
	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/budget"
	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/datasource"
	_ "github.com/bino-bi/sluice/internal/datasource/drivers/duckdbfile" // driver registration
	_ "github.com/bino-bi/sluice/internal/datasource/drivers/httpfile"   // driver registration
	_ "github.com/bino-bi/sluice/internal/datasource/drivers/motherduck" // driver registration
	_ "github.com/bino-bi/sluice/internal/datasource/drivers/mysql"      // driver registration
	_ "github.com/bino-bi/sluice/internal/datasource/drivers/postgres"   // driver registration
	_ "github.com/bino-bi/sluice/internal/datasource/drivers/s3"         // driver registration
	_ "github.com/bino-bi/sluice/internal/datasource/drivers/sqlitefile" // driver registration
	_ "github.com/bino-bi/sluice/internal/datasource/drivers/synthetic"  // driver registration
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
	"github.com/bino-bi/sluice/internal/synth/build"
	"github.com/bino-bi/sluice/internal/telemetry"
	"github.com/bino-bi/sluice/internal/transport/admin"
	"github.com/bino-bi/sluice/internal/transport/mcp"
	"github.com/bino-bi/sluice/internal/transport/rest"
	"github.com/bino-bi/sluice/internal/version"

	"github.com/bino-bi/sluice/pkg/apitypes"
)

// runtimeDeps is the fully-wired dependency graph the `serve` and `mcp`
// subcommands start and shut down. Each field is owned by the bootstrap
// and freed by Close (reverse order of construction).
type runtimeDeps struct {
	log          *slog.Logger
	server       *config.ServerConfig
	snapshot     *config.Snapshot
	policyDir    string
	registry     *config.Registry
	resolver     *secrets.Resolver
	parser       parser.Parser
	sourceReg    *datasource.Registry
	exec         *executor.Executor
	parseExec    *executor.Executor
	schemaCache  schema.Cache
	policyEng    *policy.Engine      // YAML engine; the engine or a composite member
	policyEngine policy.PolicyEngine // engine passed to queryservice (yaml or composite)
	rewrite      *rewriter.Rewriter
	auditDisp    *audit.Dispatcher
	auditSinks   []audit.Sink
	auditFile    *audit.FileSink
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
	synth        *build.Service // nil without a synthetic data source
	telShutdown  func(context.Context) error

	// mcpPinned is the credential of the stdio MCP transport of serve.
	// Nil without one.
	mcpPinned *pinnedCred

	// refuseApprovals fails a reload that loads an ApprovalPolicy. Set for
	// `sluice mcp`, which serves no approval link.
	refuseApprovals bool

	// globalLimiter and perIPLimiter are the buckets ahead of the login.
	// REST and MCP over HTTP share them, so a client has one budget for
	// both listeners. Nil means no limit.
	globalLimiter *ratelimit.KeyedLimiter
	perIPLimiter  *ratelimit.KeyedLimiter
}

// runMode selects the parts buildRuntimeFor builds.
type runMode int

const (
	// runServe builds every part `sluice serve` starts.
	runServe runMode = iota
	// runMCP builds the parts `sluice mcp` runs: no REST or admin server,
	// no approval broker and no synthetic build service. The command
	// builds its MCP server itself.
	runMCP
)

// failedBuildCloseTimeout bounds the cleanup after a failed start.
const failedBuildCloseTimeout = 10 * time.Second

// buildRuntime builds the runtime of `sluice serve`.
func buildRuntime(ctx context.Context, serverCfgPath, policyDir string) (*runtimeDeps, error) {
	return buildRuntimeFor(ctx, runServe, serverCfgPath, policyDir)
}

// buildRuntimeFor wires the layers for mode in stages: core pipeline,
// identity, query service, synthetic builds and transports. Each stage
// has a close method, and Close runs them in reverse. When a stage fails,
// Close frees what was built so far (tracer flush, audit sinks and their
// lock, executor, health loop). Errors carry a context-specific prefix so
// the caller can exit with the right code.
func buildRuntimeFor(ctx context.Context, mode runMode, serverCfgPath, policyDir string) (*runtimeDeps, error) {
	if !parserbackend.Implemented {
		return nil, fmt.Errorf("parser backend %q is a non-functional stub in this build (compiled with -tags=pure_parser); rebuild without the tag", parserbackend.Name())
	}
	deps := &runtimeDeps{refuseApprovals: mode == runMCP}
	stages := []func() error{
		func() error { return deps.buildCore(ctx, serverCfgPath, policyDir) },
		func() error { return deps.buildIdentityStage(ctx) },
		func() error { return deps.buildService(ctx, mode) },
		func() error { return deps.buildSynth(mode) },
		func() error { return deps.buildTransports(ctx, mode) },
	}
	for _, stage := range stages {
		if err := stage(); err != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), failedBuildCloseTimeout)
			defer cancel()
			deps.Close(closeCtx)
			return nil, err
		}
	}
	return deps, nil
}

// buildCore builds the server config, telemetry, secrets, snapshot,
// parser, data sources, executors, schema cache, policy engine, rewriter
// and audit. closeCore frees them.
func (d *runtimeDeps) buildCore(ctx context.Context, serverCfgPath, policyDir string) error {
	// Validate refuses settings this build cannot enforce: an operator
	// must never believe a control is active when it is not.
	scfg, err := config.LoadServer(serverCfgPath, nil)
	if err != nil {
		return fmt.Errorf("load server config: %w", err)
	}
	// A typo such as rest.tsl would otherwise keep the default silently.
	if serverCfgPath != "" {
		if err := config.CheckServerKeys(serverCfgPath); err != nil {
			return err
		}
	}
	if err := scfg.Validate(); err != nil {
		return fmt.Errorf("server config: %w", err)
	}
	d.server = scfg

	// Telemetry (slog + optional prom gauge + optional otel tracing).
	telCfg := telemetry.DefaultConfig(telemetry.ServiceInfo{
		Name:    "sluice",
		Version: version.Current().Version,
		Commit:  version.Current().Commit,
	})
	telCfg.Logging.Level = parseLogLevel(scfg.Logging.Level)
	telCfg.Logging.Format = strings.ToLower(scfg.Logging.Format)
	telCfg.Logging.MaxErrorBytes = scfg.Logging.MaxErrorBytes
	telCfg.Metrics.Enabled = scfg.Admin.Enabled
	telCfg.Tracing = telemetry.TracingConfig{
		Enabled:     scfg.Tracing.Enabled,
		Endpoint:    scfg.Tracing.Endpoint,
		Protocol:    scfg.Tracing.Protocol,
		Insecure:    scfg.Tracing.Insecure,
		SampleRatio: scfg.Tracing.SampleRatio,
	}
	shutdown, err := telemetry.Init(ctx, telCfg)
	if err != nil {
		return fmt.Errorf("telemetry init: %w", err)
	}
	d.telShutdown = shutdown
	d.log = slog.Default()

	// Secrets resolver (env + file providers).
	d.resolver = secrets.NewResolver(secrets.ResolverOptions{Logger: d.log})

	// Snapshot (policies + data sources + subject bindings).
	d.policyDir = policyDir
	if d.policyDir == "" {
		d.policyDir = scfg.Policies.Directory
	}
	snap, err := config.LoadDirectory(ctx, config.LoadOptions{
		Sources: []config.SourceDir{{Path: d.policyDir}},
	})
	if err != nil {
		return fmt.Errorf("load policy directory %q: %w", d.policyDir, err)
	}
	d.snapshot = snap

	// Registry: atomic snapshot holder for downstream subscribers.
	d.registry = config.NewRegistry()
	d.registry.Publish(snap)

	d.parser = parserbackend.New(parser.Options{
		MaxSQLBytes: scfg.Limits.MaxSQLBytes,
		Logger:      d.log,
	})
	version.SetPgQueryVersion(parserbackend.Version())

	// Data-source registry (AttachHook only usable after executor init).
	d.sourceReg, err = datasource.New(ctx, datasource.Options{
		Snapshot:     &datasource.Snapshot{DataSources: snap.DataSources},
		Secrets:      secrets.NewDataSourceResolver(d.resolver),
		Logger:       d.log,
		FailFast:     scfg.DataSources.FailFast,
		SyntheticDir: scfg.Synthetic.Dir,
	})
	if err != nil {
		return fmt.Errorf("datasource registry: %w", err)
	}

	// Executor (DuckDB); installs AttachHook, which ATTACHes every
	// catalog once for the whole instance before the hardening lock.
	d.exec, err = executor.New(ctx, executor.Config{
		Path: "",
		Harden: executor.HardenConfig{
			MemoryLimit:          scfg.DuckDB.MemoryLimit,
			Threads:              scfg.DuckDB.Threads,
			TempDirectory:        scfg.DuckDB.TempDir,
			AllowedExternalPaths: d.sourceReg.ExternalPaths(),
		},
		AttachHook:              d.sourceReg.AttachHook(),
		MaxOpen:                 scfg.DuckDB.MaxOpen,
		MaxIdle:                 scfg.DuckDB.MaxIdle,
		ConnMaxIdle:             scfg.DuckDB.ConnMaxIdle,
		DefaultStatementTimeout: scfg.Limits.QueryTimeout,
		Logger:                  d.log,
	})
	if err != nil {
		return fmt.Errorf("executor: %w", err)
	}

	// Parse-only DuckDB for the rewriter's parse check. Query
	// connections stay checked out while results stream, so a check on
	// that pool would wait behind long reads. Nothing is attached here.
	d.parseExec, err = executor.NewParseCheck(ctx, scfg.Limits.MaxSQLBytes, d.log)
	if err != nil {
		return fmt.Errorf("parse check executor: %w", err)
	}

	// Health sweep pool: same *sql.DB the schema cache borrows from.
	// Kicks one immediate sweep so /v1/ready reflects real state shortly
	// after boot instead of after the first full HealthInterval.
	d.sourceReg.SetPool(ctx, datasource.NewSQLPool(d.exec.DB()))

	// Schema cache, wired to the pool via a narrow ConnProvider.
	schema.RegisterMetrics()
	d.schemaCache = schema.New(schema.Options{
		Loader: schema.NewLoader(d.sourceReg, d.exec.DB()),
		Logger: d.log,
	})

	// Policy engine(s) + snapshot apply. The YAML engine always exists
	// (admin snapshot introspection + composite member); the engine passed
	// to the queryservice may be a composite.
	policy.RegisterMetrics()
	d.policyEng, d.policyEngine, err = buildPolicyEngine(scfg, d.resolver, d.log)
	if err != nil {
		return fmt.Errorf("policy engine: %w", err)
	}
	if err := d.policyEngine.ApplySnapshot(ctx, snap); err != nil {
		return fmt.Errorf("policy snapshot: %w", err)
	}

	defaultDB, err := d.exec.CurrentDatabase(ctx)
	if err != nil {
		return fmt.Errorf("executor: %w", err)
	}
	d.rewrite = rewriter.New(rewriter.Options{
		Parser:          d.parser,
		Schema:          d.schemaCache,
		Logger:          d.log,
		Salts:           secrets.NewSaltStore(d.resolver),
		ParseCheck:      d.parseExec,
		DefaultDatabase: defaultDB,
	})

	// Audit dispatcher: file sink (chain primary) plus optional
	// best-effort syslog / s3 secondaries.
	d.auditSinks, d.auditFile, err = buildAuditSinks(ctx, scfg, d.resolver, d.log)
	if err != nil {
		return err
	}
	genesis, err := resolveGenesisSeed(ctx, d.resolver, scfg.Audit.File)
	if err != nil {
		return fmt.Errorf("audit genesis: %w", err)
	}
	var chainKey []byte
	if ref := scfg.Audit.ChainKeyRef; ref != "" {
		key, err := d.resolver.ResolveString(ctx, ref)
		if err != nil {
			return fmt.Errorf("audit.chainKeyRef: %w", err)
		}
		// An empty secret would silently mean a chain without a key.
		if key == "" {
			return fmt.Errorf("audit.chainKeyRef: the secret %s is empty", ref)
		}
		chainKey = []byte(key)
	}
	host, _ := os.Hostname()
	d.auditDisp, err = audit.NewDispatcher(audit.DispatcherOptions{
		Sinks:       d.auditSinks,
		GenesisSeed: genesis,
		ChainKey:    chainKey,
		Origin:      host,
		Logger:      d.log,
	})
	if err != nil {
		return fmt.Errorf("audit dispatcher: %w", err)
	}
	return nil
}

// closeCore frees what buildCore built. Missing parts are skipped.
func (d *runtimeDeps) closeCore(ctx context.Context) {
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
	if d.parseExec != nil {
		_ = d.parseExec.Close()
	}
	if d.telShutdown != nil {
		_ = d.telShutdown(ctx)
	}
}

// buildIdentityStage builds the identifiers (JWT via SubjectBindings plus
// the optional API-key pepper) and the per-subject rate limiter. Nothing
// in it needs a close.
func (d *runtimeDeps) buildIdentityStage(ctx context.Context) error {
	ids, err := buildIdentity(ctx, d.server, d.resolver, d.snapshot, d.log)
	if err != nil {
		return fmt.Errorf("identity: %w", err)
	}
	d.identifier = ids.identifier
	d.apikey = ids.apikey
	d.jwtID = ids.jwt
	d.jwtBindings = ids.bindings

	// Per-subject rate limiter (from SubjectBinding.spec.rateLimit,
	// falling back to limits.defaultSubjectRps for bindings without one).
	d.rateLimiter = ratelimit.New(nil)
	d.rateLimiter.SetDefault(ratelimit.Spec{
		RPS:   d.server.Limits.DefaultSubjectRPS,
		Burst: d.server.Limits.DefaultSubjectBurst,
	})
	d.rateLimiter.SetSpecs(ratelimit.SpecsFromBindings(d.snapshot.SubjectBindings))
	return nil
}

// buildService builds the rewrite cache, the approval broker (serve
// only), the budget manager and the query service. closeService frees
// them.
func (d *runtimeDeps) buildService(ctx context.Context, mode runMode) error {
	scfg, snap := d.server, d.snapshot

	// Rewrite cache (opt-in). Built before the queryservice so the reload
	// can Purge it on every snapshot publish.
	if scfg.Cache.Rewrite.Enabled {
		d.rewriteCache = policycache.New(scfg.Cache.Rewrite.Size, scfg.Cache.Rewrite.TTL)
	}

	var err error
	if mode == runMCP {
		// An approver decides over the REST API, which `sluice mcp` does
		// not start. A held query could never be released.
		if len(snap.ByKind[apitypes.KindApprovalPolicy]) > 0 {
			return errors.New("an ApprovalPolicy is loaded, and `sluice mcp` serves no approval link: run `sluice serve` with mcp.enabled instead")
		}
	} else {
		// Approval broker (built before the queryservice so it can share
		// the instance). Active when at least one ApprovalPolicy is loaded
		// or a public base URL is configured; policies without a base URL
		// fail startup fail-closed. The sync wait is clamped to fit the
		// REST request timeout so a hybrid wait returns
		// ERR_APPROVAL_PENDING rather than being killed by the timeout
		// middleware.
		approvalSyncClamp(scfg, d.log)
		d.approvals, err = buildApprovalBroker(ctx, scfg, snap, d.resolver, d.auditDisp, d.log)
		if err != nil {
			return fmt.Errorf("approval broker: %w", err)
		}
	}

	// Per-subject budget manager (opt-in, SQLite-backed).
	d.budget, err = buildBudgetManager(scfg, snap, d.log)
	if err != nil {
		return fmt.Errorf("budget manager: %w", err)
	}

	qopts := queryservice.Options{
		Parser:          d.parser,
		Policy:          d.policyEngine,
		Rewriter:        d.rewrite,
		Executor:        d.exec,
		Audit:           d.auditDisp,
		Schema:          d.schemaCache,
		Logger:          d.log,
		RateLimiter:     d.rateLimiter,
		AuditBestEffort: !scfg.Audit.FailClosed,
		Keys:            secrets.NewKeyStore(d.resolver),
		Salts:           secrets.NewSaltStore(d.resolver),
		Catalogs:        d.sourceReg.Catalogs,
		SourceDown: func(catalog string) bool {
			for _, st := range d.sourceReg.Statuses() {
				if strings.EqualFold(st.Name, catalog) {
					return !st.Healthy
				}
			}
			return false
		},
		Limits: queryservice.Limits{
			DefaultMaxRows:         scfg.Limits.MaxRows,
			MaxRowsCeiling:         scfg.Limits.MaxRowsCeiling,
			MaxResultBytes:         scfg.Limits.MaxResultBytes,
			DefaultTimeout:         scfg.Limits.QueryTimeout,
			MaxTimeout:             scfg.Limits.MaxQueryTimeout,
			MaxSQLBytes:            scfg.Limits.MaxSQLBytes,
			MaxConcurrent:          scfg.Limits.MaxConcurrent,
			DisableCrossCatalog:    scfg.Limits.DisableCrossCatalog,
			SQLSampleBytes:         scfg.Audit.SQLSampleBytes,
			ApprovalSQLSampleBytes: scfg.Approval.SQLSampleBytes,
			SampleMaxRows:          scfg.Sample.MaxRows,
			SampleMinGroupRows:     scfg.Sample.MinGroupRows,
		},
	}
	if d.rewriteCache != nil {
		qopts.Cache = d.rewriteCache
	}
	if d.approvals != nil {
		qopts.Approvals = d.approvals
		qopts.ApprovalSyncWait = scfg.Approval.SyncWait
	}
	if d.budget != nil {
		qopts.Budget = d.budget
	}
	d.service = queryservice.New(qopts)
	return nil
}

// closeService frees what buildService built. Missing parts are skipped.
func (d *runtimeDeps) closeService(ctx context.Context) {
	if d.budget != nil {
		// Final flush so the last window of usage is persisted.
		_ = d.budget.Close(ctx)
	}
	if d.approvals != nil {
		// Before the dispatcher: the broker's auditor writes through it.
		_ = d.approvals.Close(ctx)
	}
}

// buildSynth builds the synthetic build service of serve. Only with a
// synthetic data source, so its metrics exist only then. closeSynth
// stops it.
func (d *runtimeDeps) buildSynth(mode runMode) error {
	if mode != runServe || !hasSynthetic(d.snapshot.DataSources) {
		return nil
	}
	scfg := d.server
	build.RegisterMetrics()
	d.synth = build.NewService(build.ServiceOptions{
		DataSources: d.snapshot.DataSources,
		Tagger: func() (build.Tagger, error) {
			return policy.NewColumnTagger(d.registry.Current())
		},
		Secrets:      secrets.NewDataSourceResolver(d.resolver),
		Dir:          scfg.Synthetic.Dir,
		MaxRatio:     scfg.Synthetic.MaxRatio,
		BuildTimeout: scfg.Synthetic.BuildTimeout,
		Harden: executor.HardenConfig{
			MemoryLimit:   scfg.DuckDB.MemoryLimit,
			Threads:       scfg.DuckDB.Threads,
			TempDirectory: scfg.DuckDB.TempDir,
		},
		Audit:      d.auditDisp,
		FailClosed: scfg.Audit.FailClosed,
		Registry:   d.sourceReg,
		Exec:       d.exec,
		Schema:     d.schemaCache,
		Log:        d.log,
	})
	return nil
}

// closeSynth stops the synthetic builds. It runs before closeService and
// closeCore: the end record of a canceled build goes through the
// dispatcher, and a build may be swapping in the registry and executor.
func (d *runtimeDeps) closeSynth(ctx context.Context) {
	if d.synth == nil {
		return
	}
	if err := d.synth.Close(ctx); err != nil {
		d.log.Warn("synthetic: builds did not stop in time", "error", err)
	}
}

// buildTransports builds the limiters ahead of the login, the config
// watcher and, for serve, the REST server, the MCP server of mcp.enabled
// and the admin server. closeTransports frees them.
func (d *runtimeDeps) buildTransports(ctx context.Context, mode runMode) error {
	scfg := d.server

	// Transport-level buckets sit ahead of identity on /v1/query and on
	// MCP over HTTP; zero RPS disables them (limits.* is restart-only,
	// like the other limits).
	if scfg.Limits.GlobalRPS > 0 {
		d.globalLimiter = ratelimit.NewKeyed(
			ratelimit.Spec{RPS: scfg.Limits.GlobalRPS, Burst: scfg.Limits.GlobalBurst}, 1, nil)
	}
	if scfg.Limits.PerIPRPS > 0 {
		d.perIPLimiter = ratelimit.NewKeyed(
			ratelimit.Spec{RPS: scfg.Limits.PerIPRPS, Burst: scfg.Limits.PerIPBurst},
			scfg.Limits.PerIPMaxBuckets, nil)
	}

	if mode == runServe {
		if err := d.buildServeListeners(ctx); err != nil {
			return err
		}
	}

	// Config watcher: fsnotify + SIGHUP + admin /reload all funnel
	// through here, republishing the snapshot to subscribers on change.
	// Always constructed: policies.reload gates only the fsnotify loop
	// (startReloads); the synchronous Reload path backing SIGHUP and
	// POST /admin/reload works without it.
	var err error
	d.watcher, err = config.NewWatcher(config.WatchOptions{
		Dir:       d.policyDir,
		ModuleDir: scfg.Policies.OPA.ModuleDir,
		Registry:  d.registry,
		Logger:    d.log,
		// All or nothing: policies of every engine, identity, limits and
		// budgets are prepared before anything is published.
		Validate: d.prepareReload,
	})
	if err != nil {
		return fmt.Errorf("config watcher: %w", err)
	}

	if mode != runServe || !scfg.Admin.Enabled {
		return nil
	}
	token, err := resolveAdminToken(ctx, d.resolver, scfg.Admin.Token)
	if err != nil {
		return err
	}
	if err := checkAdminAnonymous(scfg.Admin, token); err != nil {
		return err
	}
	adminCfg := admin.Config{
		Enabled: true,
		Listen:  scfg.Admin.Listen,
		Token:   token,
	}
	if t := scfg.Admin.TLS; t != nil {
		adminCfg.TLS = &admin.TLSConfig{CertFile: t.CertFile, KeyFile: t.KeyFile, ClientCA: t.ClientCA}
	}
	d.admin = admin.New(adminCfg, admin.Deps{
		Service:   d.service,
		Approvals: adminPendingLister(d.approvals),
		Snapshots: d.registry,
		Sources:   d.sourceReg,
		Catalogs:  registryCatalogLister{r: d.sourceReg},
		Logger:    d.log,
		Reloader:  reloaderFromWatcher(d.watcher),
		Audit:     auditTailer(d.auditFile),
		Synth:     adminSynthService(d.synth),

		AuthFailed: d.authFailed("admin"),
	})
	return nil
}

// buildServeListeners builds the REST server of serve and its MCP server
// when mcp.enabled.
func (d *runtimeDeps) buildServeListeners(ctx context.Context) error {
	scfg := d.server
	restCfg := rest.Config{
		Listen:         scfg.REST.Listen,
		MaxBodyBytes:   scfg.REST.MaxBodyBytes,
		RequestTimeout: scfg.REST.RequestTimeout,
		Tracing:        scfg.Tracing.Enabled,
	}
	// Validate guarantees cert+key are both set when the block exists.
	if t := scfg.REST.TLS; t != nil {
		restCfg.TLS = &rest.TLSConfig{CertFile: t.CertFile, KeyFile: t.KeyFile, ClientCA: t.ClientCA}
	}
	restDeps := rest.Deps{
		Service:    d.service,
		Identifier: d.identifier,
		Registry:   d.sourceReg,
		Approvals:  approvalGateway(d.approvals),
		Logger:     d.log,
		AuthFailed: d.authFailed("rest"),
	}
	// A nil pointer in the interface field would not read as "no limit".
	if d.globalLimiter != nil {
		restDeps.GlobalLimiter = d.globalLimiter
	}
	if d.perIPLimiter != nil {
		restDeps.PerIPLimiter = d.perIPLimiter
	}
	d.rest = rest.New(restCfg, restDeps)

	if !scfg.MCP.Enabled {
		return nil
	}
	var err error
	d.mcpPinned, err = resolveMCPPinnedUser(ctx, scfg, d.resolver, d.identifier, d.log)
	if err != nil {
		return fmt.Errorf("mcp: %w", err)
	}
	var pinned *identity.UserCtx
	if d.mcpPinned != nil {
		pinned = d.mcpPinned.user
	}
	d.mcp, err = newMCPServer(d, mcp.TransportMode(scfg.MCP.Transport), scfg.MCP.AllowAnonymous, pinned)
	if err != nil {
		return fmt.Errorf("mcp: %w", err)
	}
	return nil
}

// closeTransports stops the config watcher, so no reload runs during the
// rest of Close. The servers stop with the context of their run.
func (d *runtimeDeps) closeTransports() {
	if d.watcher != nil {
		_ = d.watcher.Close()
	}
}

// authFailed returns the hook a transport calls for a request that fails
// the login. It writes an auth-event audit record with origin.
func (d *runtimeDeps) authFailed(origin string) func(remoteIP, reason string) {
	return func(remoteIP, reason string) {
		d.auditDisp.AuthFailure(origin, remoteIP, reason)
	}
}

// prepareReload does every step of a reload of snap that can fail and
// returns the apply that swaps all parts in. A part that fails fails the
// reload, so the gateway never runs on a mix of two snapshots and the
// caller of the reload sees the error. Nil guards on every dep keep the
// method usable from minimal test fixtures.
func (d *runtimeDeps) prepareReload(ctx context.Context, snap *config.Snapshot) (config.Prepared, error) {
	if d.resolver != nil {
		// Read rotated hashRef / hmacSecretRef / ReBAC tokenRef values
		// again.
		d.resolver.Invalidate()
	}
	if d.refuseApprovals && len(snap.ByKind[apitypes.KindApprovalPolicy]) > 0 {
		return config.Prepared{}, errors.New("an ApprovalPolicy is loaded, and `sluice mcp` serves no approval link")
	}
	var steps []func()
	if d.policyEngine != nil {
		apply, err := d.policyEngine.Prepare(ctx, snap)
		if err != nil {
			return config.Prepared{}, err
		}
		steps = append(steps, apply)
	}
	if d.apikey != nil {
		bindings, err := identity.APIKeyBindings(ctx, d.resolver, snap.SubjectBindings, d.log)
		if err != nil {
			return config.Prepared{}, fmt.Errorf("api-key bindings: %w", err)
		}
		steps = append(steps, func() { d.apikey.SetBindings(bindings) })
	}
	if d.jwtID != nil && d.jwtBindings != nil {
		hmac, err := identity.HMACSecrets(ctx, d.resolver, snap.SubjectBindings)
		if err != nil {
			return config.Prepared{}, fmt.Errorf("jwt bindings: %w", err)
		}
		applyBindings, err := d.jwtBindings.Prepare(derefBindings(snap.SubjectBindings))
		if err != nil {
			return config.Prepared{}, fmt.Errorf("jwt bindings: %w", err)
		}
		// Bindings first: at most a brief window of new bindings with old
		// secrets, never the reverse.
		steps = append(steps, applyBindings, func() { d.jwtID.SetHMACSecrets(hmac) })
	}
	if d.rateLimiter != nil {
		bySub, byIss := ratelimit.SpecsFromBindings(snap.SubjectBindings)
		steps = append(steps, func() { d.rateLimiter.SetSpecs(bySub, byIss) })
	}
	if d.budget != nil {
		bySub, byIss := budget.SpecsFromBindings(snap.SubjectBindings)
		steps = append(steps, func() { d.budget.SetSpecs(bySub, byIss) })
	}
	if d.rewriteCache != nil {
		steps = append(steps, d.rewriteCache.Purge)
	}
	if d.schemaCache != nil {
		steps = append(steps, d.schemaCache.InvalidateAll)
	}

	var warnings []string
	// Data sources are attached once at start.
	if d.snapshot != nil && dataSourcesChanged(d.snapshot.DataSources, snap.DataSources) {
		warnings = append(warnings, "restart needed: data sources changed")
	}
	// The broker exists only when approval.publicBaseUrl was set at start.
	// Matching queries fail with ERR_INTERNAL.
	if d.approvals == nil && len(snap.ByKind[apitypes.KindApprovalPolicy]) > 0 {
		warnings = append(warnings, "restart needed: ApprovalPolicies need approval.publicBaseUrl")
	}
	return config.Prepared{
		Apply: func() {
			for _, step := range steps {
				step()
			}
		},
		Warnings: warnings,
	}, nil
}

// dataSourcesChanged reports whether next declares other data sources
// than running, by name and content.
func dataSourcesChanged(running, next []*apitypes.DataSource) bool {
	index := func(list []*apitypes.DataSource) map[string]*apitypes.DataSource {
		out := make(map[string]*apitypes.DataSource, len(list))
		for _, ds := range list {
			if ds != nil {
				out[ds.Metadata.Name] = ds
			}
		}
		return out
	}
	return !reflect.DeepEqual(index(running), index(next))
}

// auditTailer adapts the concrete file sink to the admin.AuditTailer
// interface. The typed-nil guard keeps a nil *FileSink from becoming a
// non-nil interface that panics on use.
func auditTailer(fs *audit.FileSink) admin.AuditTailer {
	if fs == nil {
		return nil
	}
	return fs
}

func hasSynthetic(sources []*apitypes.DataSource) bool {
	for _, ds := range sources {
		if ds != nil && ds.Spec.Type == apitypes.DSSynthetic {
			return true
		}
	}
	return false
}

// adminSynth gives admin builds the origin admin. The typed-nil guard
// keeps a nil service from becoming a non-nil interface.
type adminSynth struct{ svc *build.Service }

func adminSynthService(svc *build.Service) admin.SynthService {
	if svc == nil {
		return nil
	}
	return adminSynth{svc: svc}
}

// Start implements admin.SynthService.
func (a adminSynth) Start(ctx context.Context, name, remoteIP string) (build.BuildView, bool, error) {
	return a.svc.Start(ctx, name, build.OriginAdmin, remoteIP)
}

// Cancel implements admin.SynthService.
func (a adminSynth) Cancel(name string) (build.BuildView, error) { return a.svc.Cancel(name) }

// Status implements admin.SynthService.
func (a adminSynth) Status(name string) (build.StatusView, error) { return a.svc.Status(name) }

// reloaderFromWatcher adapts the config.Watcher.Reload signature to the
// admin.Reloader interface. Nil is returned when the watcher is disabled
// so /admin/reload responds 501 rather than silently succeeding.
func reloaderFromWatcher(w *config.Watcher) admin.Reloader {
	if w == nil {
		return nil
	}
	return reloaderFunc(w.Reload)
}

type reloaderFunc func(ctx context.Context) ([]string, error)

// Reload implements admin.Reloader.
func (f reloaderFunc) Reload(ctx context.Context) ([]string, error) { return f(ctx) }

// Close releases every resource in reverse construction order: the
// stage closers run from the last stage to the first. Safe to call on a
// partial graph: missing components are skipped.
func (d *runtimeDeps) Close(ctx context.Context) {
	if d == nil {
		return
	}
	d.closeTransports()
	d.closeSynth(ctx)
	d.closeService(ctx)
	d.closeCore(ctx)
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
		seed, err := r.ResolveString(ctx, file.Genesis)
		return []byte(seed), err
	}
	host, _ := os.Hostname()
	return []byte("sluice-genesis:" + host + ":" + version.Current().CommitFull), nil
}

// resolveAdminToken returns the admin token. A value that starts with
// "secret://" is a reference, anything else is the token itself. An empty
// secret is an error: an empty token turns the check of the admin API off.
func resolveAdminToken(ctx context.Context, r *secrets.Resolver, token string) (string, error) {
	if !secrets.IsRef(token) {
		return token, nil
	}
	resolved, err := r.ResolveString(ctx, token)
	if err != nil {
		return "", fmt.Errorf("resolve admin.token: %w", err)
	}
	if resolved == "" {
		return "", fmt.Errorf("admin.token: the secret is empty")
	}
	return resolved, nil
}

// checkAdminAnonymous refuses an admin API without a token unless the
// operator opts in or the listener is loopback only. One missing
// SLUICE_ADMIN__TOKEN must not open reload and audit to the network.
func checkAdminAnonymous(a config.AdminConfig, token string) error {
	if token != "" || a.AllowAnonymous || loopbackOnly(a.Listen) {
		return nil
	}
	return fmt.Errorf("admin: admin.token is empty and admin.listen %q is not loopback only; set admin.token, bind admin.listen to 127.0.0.1, or set admin.allowAnonymous: true", a.Listen)
}

// loopbackOnly reports whether listen binds to a loopback address only.
// An empty host binds every interface.
func loopbackOnly(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
	hmacSecrets, err := identity.HMACSecrets(ctx, r, snap.SubjectBindings)
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
		pepperText, err := r.ResolveString(ctx, scfg.Identity.APIKeyPepper)
		if err != nil {
			return identityStack{}, fmt.Errorf("resolve apiKeyPepper: %w", err)
		}
		if pepperText == "" {
			return identityStack{}, fmt.Errorf("identity.apiKeyPepper: the secret is empty")
		}
		pepper := []byte(pepperText)
		bindings, err := identity.APIKeyBindings(ctx, r, snap.SubjectBindings, log)
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
