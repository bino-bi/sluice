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
}

// AuditConfig selects audit sinks. MVP ships file only; richer sinks land in v1.
type AuditConfig struct {
	File *FileSinkConfig `mapstructure:"file"`

	// FailClosed refuses to serve a query when its access audit record
	// cannot be durably enqueued. Default true — audit-first posture. Set
	// to false to fall back to best-effort auditing (serve on audit drop).
	FailClosed bool `mapstructure:"failClosed"`
}

// FileSinkConfig configures the append-only audit log file.
type FileSinkConfig struct {
	Path         string `mapstructure:"path"`
	RotateDaily  bool   `mapstructure:"rotateDaily"`
	RotateSizeMB int    `mapstructure:"rotateSizeMB"`
	Genesis      string `mapstructure:"genesis"` // secret:// ref
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

// LimitsConfig bundles request-size / concurrency / cross-catalog switches.
type LimitsConfig struct {
	MaxRows             int64         `mapstructure:"maxRows"`
	MaxRowsCeiling      int64         `mapstructure:"maxRowsCeiling"`
	MaxSQLBytes         int           `mapstructure:"maxSqlBytes"`
	QueryTimeout        time.Duration `mapstructure:"queryTimeout"`
	MaxQueryTimeout     time.Duration `mapstructure:"maxQueryTimeout"`
	MaxConcurrent       int           `mapstructure:"maxConcurrent"`
	DisableCrossCatalog bool          `mapstructure:"disableCrossCatalog"`
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
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
		Audit: AuditConfig{
			FailClosed: true,
		},
		Limits: LimitsConfig{
			MaxRows:         100_000,
			MaxRowsCeiling:  100_000,
			MaxSQLBytes:     1 << 20,
			QueryTimeout:    30 * time.Second,
			MaxQueryTimeout: 30 * time.Second,
			MaxConcurrent:   100,
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

	v.SetDefault("logging.level", d.Logging.Level)
	v.SetDefault("logging.format", d.Logging.Format)

	v.SetDefault("audit.failClosed", d.Audit.FailClosed)
	v.SetDefault("limits.maxRows", d.Limits.MaxRows)
	v.SetDefault("limits.maxRowsCeiling", d.Limits.MaxRowsCeiling)
	v.SetDefault("limits.maxSqlBytes", d.Limits.MaxSQLBytes)
	v.SetDefault("limits.queryTimeout", d.Limits.QueryTimeout)
	v.SetDefault("limits.maxQueryTimeout", d.Limits.MaxQueryTimeout)
	v.SetDefault("limits.maxConcurrent", d.Limits.MaxConcurrent)
	v.SetDefault("limits.disableCrossCatalog", d.Limits.DisableCrossCatalog)
}
