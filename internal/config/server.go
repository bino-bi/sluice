// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// ServerConfig is the top-level runtime configuration loaded from sluice.yaml.
// All fields have sensible defaults so an empty YAML file still yields a
// usable (if minimal) server configuration.
type ServerConfig struct {
	REST        RESTConfig        `mapstructure:"rest"`
	MCP         MCPConfig         `mapstructure:"mcp"`
	Admin       AdminConfig       `mapstructure:"admin"`
	DuckDB      DuckDBConfig      `mapstructure:"duckdb"`
	DataSources DataSourcesConfig `mapstructure:"datasources"`
	Policies    PoliciesConfig    `mapstructure:"policies"`
	Audit       AuditConfig       `mapstructure:"audit"`
	Logging     LoggingConfig     `mapstructure:"logging"`
	Identity    IdentityConfig    `mapstructure:"identity"`
	Limits      LimitsConfig      `mapstructure:"limits"`
	Cache       CacheConfig       `mapstructure:"cache"`
	Approval    ApprovalConfig    `mapstructure:"approval"`
	Budget      BudgetConfig      `mapstructure:"budget"`
}

// RESTConfig configures the public REST transport. TLS is MVP-optional.
type RESTConfig struct {
	Listen         string        `mapstructure:"listen"`
	TLS            *TLSConfig    `mapstructure:"tls"`
	MaxBodyBytes   int64         `mapstructure:"maxBodyBytes"`
	RequestTimeout time.Duration `mapstructure:"requestTimeout"`
}

// MCPConfig configures the Model Context Protocol transport.
type MCPConfig struct {
	Enabled        bool          `mapstructure:"enabled"`
	Transport      string        `mapstructure:"transport"` // "stdio" | "streamable_http"
	Listen         string        `mapstructure:"listen"`
	SessionIdleMax time.Duration `mapstructure:"sessionIdleMax"`

	// TokenRef is a secret:// reference to a static JWT bearer token that
	// pins the identity of the serve-embedded stdio transport. Resolved
	// once at boot through the same identity pipeline as the HTTP
	// transports.
	TokenRef string `mapstructure:"tokenRef"`
	// APIKeyRef is a secret:// reference to a static API key
	// ("<id>.<secret>") used the same way. When both are set, the JWT is
	// tried first.
	APIKeyRef string `mapstructure:"apiKeyRef"`
	// AllowAnonymous lets the MCP transport run without a credential:
	// stdio pins the anonymous subject; streamable_http admits
	// credential-less requests as anonymous instead of rejecting with 401.
	AllowAnonymous bool `mapstructure:"allowAnonymous"`
}

// AdminConfig configures the admin transport (metrics, reload, health).
type AdminConfig struct {
	Enabled bool       `mapstructure:"enabled"`
	Listen  string     `mapstructure:"listen"`
	Token   string     `mapstructure:"token"`
	TLS     *TLSConfig `mapstructure:"tls"`
}

// DuckDBConfig configures the embedded DuckDB engine. MaxOpen/MaxIdle are
// Go sql.DB pool settings; MemoryLimit/Threads/TempDir are DuckDB PRAGMAs.
type DuckDBConfig struct {
	MemoryLimit string        `mapstructure:"memoryLimit"`
	Threads     int           `mapstructure:"threads"`
	TempDir     string        `mapstructure:"tempDir"`
	MaxOpen     int           `mapstructure:"maxOpen"`
	MaxIdle     int           `mapstructure:"maxIdle"`
	ConnMaxIdle time.Duration `mapstructure:"connMaxIdle"`
}

// DataSourcesConfig locates the directory of DataSource YAML manifests.
type DataSourcesConfig struct {
	Directory string `mapstructure:"directory"`
	FailFast  bool   `mapstructure:"failFast"`
	Reload    bool   `mapstructure:"reload"`
}

// PoliciesConfig locates the directory of policy YAML manifests.
type PoliciesConfig struct {
	Directory string `mapstructure:"directory"`
	Reload    bool   `mapstructure:"reload"`
	// Engine selects the policy-decision engine: "yaml" (default), "opa",
	// or "composite". Composite fans out across Composite.Members.
	Engine    string          `mapstructure:"engine"`
	Composite CompositeConfig `mapstructure:"composite"`
	OPA       OPAConfig       `mapstructure:"opa"`
	Rebac     RebacConfig     `mapstructure:"rebac"`
}

// CompositeConfig lists the ordered engine members for engine=composite.
type CompositeConfig struct {
	Members []string `mapstructure:"members"`
}

// OPAConfig configures the embedded OPA engine.
type OPAConfig struct {
	ModuleDir string `mapstructure:"moduleDir"`
	Query     string `mapstructure:"query"`
}

// RebacConfig configures the ReBAC engine's check cache.
type RebacConfig struct {
	CacheTTL  time.Duration `mapstructure:"cacheTtl"`
	CacheSize int           `mapstructure:"cacheSize"`
}

// AuditConfig selects audit sinks. The file sink is the durable,
// hash-chained record; syslog and s3 are best-effort secondary sinks that
// receive every record but never gate a query.
type AuditConfig struct {
	File   *FileSinkConfig   `mapstructure:"file"`
	Syslog *SyslogSinkConfig `mapstructure:"syslog"` // nil = disabled
	S3     *S3SinkConfig     `mapstructure:"s3"`     // nil = disabled

	// FailClosed refuses to serve a query when its access audit record
	// cannot be durably enqueued. Default true — audit-first posture. Set
	// to false to fall back to best-effort auditing (serve on audit drop).
	FailClosed bool `mapstructure:"failClosed"`

	// SQLSampleBytes caps the leading bytes of request SQL copied into
	// each audit record's sql_sample. 0 disables sampling entirely.
	SQLSampleBytes int `mapstructure:"sqlSampleBytes"`
}

// FileSinkConfig configures the append-only audit log file.
type FileSinkConfig struct {
	Path         string `mapstructure:"path"`
	RotateDaily  bool   `mapstructure:"rotateDaily"`
	RotateSizeMB int    `mapstructure:"rotateSizeMB"`
	Genesis      string `mapstructure:"genesis"` // secret:// ref
}

// SyslogSinkConfig forwards each audit record to a syslog daemon
// (RFC 5424; octet-counted on stream transports). Fire-and-forget: the
// file sink remains the durable record.
type SyslogSinkConfig struct {
	Network  string `mapstructure:"network"`  // udp (default) | tcp | unix | unixgram
	Address  string `mapstructure:"address"`  // host:port or socket path; required
	Facility string `mapstructure:"facility"` // local0..local7, daemon, auth, ...; default local0
	Tag      string `mapstructure:"tag"`      // RFC 5424 APP-NAME; default "sluice"
}

// S3SinkConfig batches audit records into newline-delimited JSON objects
// in an S3(-compatible) bucket, optionally under Object Lock retention.
type S3SinkConfig struct {
	Endpoint       string        `mapstructure:"endpoint"`       // default s3.amazonaws.com
	Bucket         string        `mapstructure:"bucket"`         // required
	Prefix         string        `mapstructure:"prefix"`         // default "audit/"
	Region         string        `mapstructure:"region"`
	Insecure       bool          `mapstructure:"insecure"`       // plain HTTP (dev / MinIO)
	ForcePathStyle bool          `mapstructure:"forcePathStyle"` // path-style addressing (MinIO)
	ObjectLock     string        `mapstructure:"objectLock"`     // "" | governance | compliance
	RetentionDays  int           `mapstructure:"retentionDays"`  // required when objectLock is set
	CredentialsRef string        `mapstructure:"credentialsRef"` // secret:// JSON; empty = env/IAM chain
	UploadInterval time.Duration `mapstructure:"uploadInterval"` // default 30s
	UploadBytes    int           `mapstructure:"uploadBytes"`    // default 1 MiB
	MaxBufferBytes int           `mapstructure:"maxBufferBytes"` // default 8 MiB
}

// LoggingConfig controls slog output.
type LoggingConfig struct {
	Level  string `mapstructure:"level"`  // debug/info/warn/error
	Format string `mapstructure:"format"` // json/text
}

// IdentityConfig holds process-wide identity settings. SubjectBinding
// manifests live alongside policies.
type IdentityConfig struct {
	APIKeyPepper string `mapstructure:"apiKeyPepper"` // secret:// ref
}

// LimitsConfig bundles request-size / concurrency / rate / cross-catalog
// switches. The rate fields are token buckets: RPS refills, Burst caps.
// GlobalRPS bounds all /v1/query traffic before identity resolution;
// PerIPRPS adds a per-remote-IP bucket on the same path (off by default —
// behind a load balancer every request shares the LB's address);
// DefaultSubjectRPS applies to authenticated subjects whose binding has no
// explicit rateLimit. Zero RPS disables the respective bucket.
type LimitsConfig struct {
	MaxRows             int64         `mapstructure:"maxRows"`
	MaxRowsCeiling      int64         `mapstructure:"maxRowsCeiling"`
	MaxSQLBytes         int           `mapstructure:"maxSqlBytes"`
	QueryTimeout        time.Duration `mapstructure:"queryTimeout"`
	MaxQueryTimeout     time.Duration `mapstructure:"maxQueryTimeout"`
	MaxConcurrent       int           `mapstructure:"maxConcurrent"`
	DisableCrossCatalog bool          `mapstructure:"disableCrossCatalog"`
	GlobalRPS           float64       `mapstructure:"globalRps"`
	GlobalBurst         int           `mapstructure:"globalBurst"`
	PerIPRPS            float64       `mapstructure:"perIpRps"`
	PerIPBurst          int           `mapstructure:"perIpBurst"`
	PerIPMaxBuckets     int           `mapstructure:"perIpMaxBuckets"`
	DefaultSubjectRPS   float64       `mapstructure:"defaultSubjectRps"`
	DefaultSubjectBurst int           `mapstructure:"defaultSubjectBurst"`
}

// CacheConfig configures the optional rewrite/decision cache.
type CacheConfig struct {
	Rewrite RewriteCacheConfig `mapstructure:"rewrite"`
}

// BudgetConfig configures per-subject daily budget enforcement.
type BudgetConfig struct {
	Enabled       bool          `mapstructure:"enabled"`
	StateDir      string        `mapstructure:"stateDir"`
	FlushInterval time.Duration `mapstructure:"flushInterval"`
	FailClosed    bool          `mapstructure:"failClosed"`
	RetentionDays int           `mapstructure:"retentionDays"`
}

// ApprovalConfig configures the human-approval workflow. The feature
// activates when at least one ApprovalPolicy is loaded; PublicBaseURL is
// then required (validated at serve boot).
type ApprovalConfig struct {
	PublicBaseURL  string            `mapstructure:"publicBaseUrl"`
	Webhooks       []ApprovalWebhook `mapstructure:"webhooks"`
	SyncWait       time.Duration     `mapstructure:"syncWait"`       // in-request wait before ERR_APPROVAL_PENDING
	RequestTTL     time.Duration     `mapstructure:"requestTtl"`     // pending-request lifetime
	GrantTTL       time.Duration     `mapstructure:"grantTtl"`       // approved-grant lifetime
	MaxPending     int               `mapstructure:"maxPending"`     // cap on concurrent pending requests
	SQLSampleBytes int               `mapstructure:"sqlSampleBytes"` // webhook SQL payload cap
}

// ApprovalWebhook is one outbound approval target. HeadersRef is a
// secret:// reference to a JSON object of header name → value (e.g. an
// Authorization bearer), resolved at boot/reload.
type ApprovalWebhook struct {
	URL        string        `mapstructure:"url"`
	HeadersRef string        `mapstructure:"headersRef"`
	Timeout    time.Duration `mapstructure:"timeout"`
}

// RewriteCacheConfig controls the (Decision, RewriteResult) cache. It is
// disabled by default — a conservative posture for a security proxy, where
// memoising a decision must never outlive the snapshot it was made under.
type RewriteCacheConfig struct {
	Enabled bool          `mapstructure:"enabled"`
	Size    int           `mapstructure:"size"`
	TTL     time.Duration `mapstructure:"ttl"`
}

// TLSConfig holds server TLS material. Client-auth (mTLS) is v1.
type TLSConfig struct {
	CertFile   string `mapstructure:"certFile"`
	KeyFile    string `mapstructure:"keyFile"`
	ClientCA   string `mapstructure:"clientCA"`
	ClientAuth string `mapstructure:"clientAuth"`
}

// DefaultServerConfig returns the ServerConfig populated with MVP defaults.
// Callers use this as the starting point before merging YAML/env/flags.
func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		REST: RESTConfig{
			Listen:         ":8080",
			MaxBodyBytes:   1 << 20,
			RequestTimeout: 30 * time.Second,
		},
		MCP: MCPConfig{
			Enabled:        false,
			Transport:      "stdio",
			SessionIdleMax: 30 * time.Minute,
		},
		Admin: AdminConfig{
			Enabled: false,
			Listen:  ":9091",
		},
		DuckDB: DuckDBConfig{
			MemoryLimit: "4GB",
			Threads:     0,
			MaxOpen:     4,
			MaxIdle:     2,
			ConnMaxIdle: 5 * time.Minute,
		},
		DataSources: DataSourcesConfig{
			Directory: "./datasources.d",
			FailFast:  true,
		},
		Policies: PoliciesConfig{
			Directory: "./policies.d",
			Reload:    true,
			Engine:    "yaml",
			Composite: CompositeConfig{Members: []string{"yaml"}},
			OPA:       OPAConfig{Query: "data.sluice.main"},
			Rebac:     RebacConfig{CacheTTL: 10 * time.Second, CacheSize: 10000},
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
		Audit: AuditConfig{
			FailClosed:     true,
			SQLSampleBytes: 2048,
		},
		Limits: LimitsConfig{
			MaxRows:         100_000,
			MaxRowsCeiling:  100_000,
			MaxSQLBytes:     1 << 20,
			QueryTimeout:    30 * time.Second,
			MaxQueryTimeout: 30 * time.Second,
			MaxConcurrent:   100,
			GlobalRPS:       500,
			GlobalBurst:     1000,
			PerIPMaxBuckets: 10_000,
		},
		Cache: CacheConfig{
			Rewrite: RewriteCacheConfig{
				Enabled: false,
				Size:    4096,
				TTL:     60 * time.Second,
			},
		},
		Approval: ApprovalConfig{
			SyncWait:       20 * time.Second,
			RequestTTL:     15 * time.Minute,
			GrantTTL:       5 * time.Minute,
			MaxPending:     1000,
			SQLSampleBytes: 2048,
		},
		Budget: BudgetConfig{
			Enabled:       false,
			StateDir:      "./state",
			FlushInterval: 5 * time.Second,
			FailClosed:    true,
			RetentionDays: 35,
		},
	}
}

// LoadServer reads the YAML file at path, applies environment-variable
// overrides (SLUICE_REST__LISTEN → rest.listen), and optional pflag overrides,
// then returns a fully-populated ServerConfig.
//
// A missing file is not an error: LoadServer returns DefaultServerConfig +
// env/flag overlays so `sluice` works with zero config on first run. A
// malformed file is always an error.
func LoadServer(path string, flagSet *pflag.FlagSet) (*ServerConfig, error) {
	v := viper.New()

	// Defaults are set before the YAML is read so absent keys fall back
	// cleanly. mapstructure tags drive unmarshal.
	setDefaults(v, DefaultServerConfig())

	v.SetEnvPrefix("SLUICE")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "__"))
	v.AutomaticEnv()

	if path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			var notFound viper.ConfigFileNotFoundError
			// A missing file is not an error: defaults + env + flags still
			// produce a valid config. Any other error is fatal.
			if !errors.As(err, &notFound) && !os.IsNotExist(err) {
				return nil, fmt.Errorf("config: read %s: %w", path, err)
			}
		}
	}

	if flagSet != nil {
		if err := v.BindPFlags(flagSet); err != nil {
			return nil, fmt.Errorf("config: bind flags: %w", err)
		}
	}

	cfg := DefaultServerConfig()
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("config: unmarshal: %w", err)
	}
	return &cfg, nil
}

// setDefaults mirrors DefaultServerConfig into viper's default layer so
// AutomaticEnv can find the keys it needs to replace.
func setDefaults(v *viper.Viper, d ServerConfig) {
	v.SetDefault("rest.listen", d.REST.Listen)
	v.SetDefault("rest.maxBodyBytes", d.REST.MaxBodyBytes)
	v.SetDefault("rest.requestTimeout", d.REST.RequestTimeout)

	v.SetDefault("mcp.enabled", d.MCP.Enabled)
	v.SetDefault("mcp.transport", d.MCP.Transport)
	v.SetDefault("mcp.sessionIdleMax", d.MCP.SessionIdleMax)
	v.SetDefault("mcp.tokenRef", d.MCP.TokenRef)
	v.SetDefault("mcp.apiKeyRef", d.MCP.APIKeyRef)
	v.SetDefault("mcp.allowAnonymous", d.MCP.AllowAnonymous)

	v.SetDefault("admin.enabled", d.Admin.Enabled)
	v.SetDefault("admin.listen", d.Admin.Listen)

	v.SetDefault("duckdb.memoryLimit", d.DuckDB.MemoryLimit)
	v.SetDefault("duckdb.threads", d.DuckDB.Threads)
	v.SetDefault("duckdb.maxOpen", d.DuckDB.MaxOpen)
	v.SetDefault("duckdb.maxIdle", d.DuckDB.MaxIdle)
	v.SetDefault("duckdb.connMaxIdle", d.DuckDB.ConnMaxIdle)

	v.SetDefault("datasources.directory", d.DataSources.Directory)
	v.SetDefault("datasources.failFast", d.DataSources.FailFast)

	v.SetDefault("policies.directory", d.Policies.Directory)
	v.SetDefault("policies.reload", d.Policies.Reload)
	v.SetDefault("policies.engine", d.Policies.Engine)
	v.SetDefault("policies.composite.members", d.Policies.Composite.Members)
	v.SetDefault("policies.opa.moduleDir", d.Policies.OPA.ModuleDir)
	v.SetDefault("policies.opa.query", d.Policies.OPA.Query)
	v.SetDefault("policies.rebac.cacheTtl", d.Policies.Rebac.CacheTTL)
	v.SetDefault("policies.rebac.cacheSize", d.Policies.Rebac.CacheSize)

	v.SetDefault("logging.level", d.Logging.Level)
	v.SetDefault("logging.format", d.Logging.Format)

	v.SetDefault("audit.failClosed", d.Audit.FailClosed)
	v.SetDefault("audit.sqlSampleBytes", d.Audit.SQLSampleBytes)
	v.SetDefault("limits.maxRows", d.Limits.MaxRows)
	v.SetDefault("limits.maxRowsCeiling", d.Limits.MaxRowsCeiling)
	v.SetDefault("limits.maxSqlBytes", d.Limits.MaxSQLBytes)
	v.SetDefault("limits.queryTimeout", d.Limits.QueryTimeout)
	v.SetDefault("limits.maxQueryTimeout", d.Limits.MaxQueryTimeout)
	v.SetDefault("limits.maxConcurrent", d.Limits.MaxConcurrent)
	v.SetDefault("limits.disableCrossCatalog", d.Limits.DisableCrossCatalog)
	v.SetDefault("limits.globalRps", d.Limits.GlobalRPS)
	v.SetDefault("limits.globalBurst", d.Limits.GlobalBurst)
	v.SetDefault("limits.perIpRps", d.Limits.PerIPRPS)
	v.SetDefault("limits.perIpBurst", d.Limits.PerIPBurst)
	v.SetDefault("limits.perIpMaxBuckets", d.Limits.PerIPMaxBuckets)
	v.SetDefault("limits.defaultSubjectRps", d.Limits.DefaultSubjectRPS)
	v.SetDefault("limits.defaultSubjectBurst", d.Limits.DefaultSubjectBurst)

	v.SetDefault("cache.rewrite.enabled", d.Cache.Rewrite.Enabled)
	v.SetDefault("cache.rewrite.size", d.Cache.Rewrite.Size)
	v.SetDefault("cache.rewrite.ttl", d.Cache.Rewrite.TTL)

	v.SetDefault("approval.publicBaseUrl", d.Approval.PublicBaseURL)
	v.SetDefault("approval.syncWait", d.Approval.SyncWait)
	v.SetDefault("approval.requestTtl", d.Approval.RequestTTL)
	v.SetDefault("approval.grantTtl", d.Approval.GrantTTL)
	v.SetDefault("approval.maxPending", d.Approval.MaxPending)
	v.SetDefault("approval.sqlSampleBytes", d.Approval.SQLSampleBytes)

	v.SetDefault("budget.enabled", d.Budget.Enabled)
	v.SetDefault("budget.stateDir", d.Budget.StateDir)
	v.SetDefault("budget.flushInterval", d.Budget.FlushInterval)
	v.SetDefault("budget.failClosed", d.Budget.FailClosed)
	v.SetDefault("budget.retentionDays", d.Budget.RetentionDays)
}
