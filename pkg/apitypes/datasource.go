// SPDX-License-Identifier: Apache-2.0

package apitypes

// DataSource declares an underlying database/storage that sluice attaches
// and exposes to clients through policy-mediated SQL.
type DataSource struct {
	TypeMeta `yaml:",inline" json:",inline"`
	Metadata ObjectMeta     `yaml:"metadata" json:"metadata"`
	Spec     DataSourceSpec `yaml:"spec" json:"spec"`
}

// DataSourceSpec carries per-type configuration. Fields that are unused
// for the chosen Type are ignored; validation in the decoder enforces
// which fields are required per type.
type DataSourceSpec struct {
	Type DataSourceType `yaml:"type" json:"type"`

	// postgres / mysql
	Connection     string `yaml:"connection,omitempty" json:"connection,omitempty"`
	CredentialsRef string `yaml:"credentialsRef,omitempty" json:"credentialsRef,omitempty"`

	// sqlite / duckdb_file
	Path string `yaml:"path,omitempty" json:"path,omitempty"`

	// s3_parquet
	Bucket       string   `yaml:"bucket,omitempty" json:"bucket,omitempty"`
	Prefix       string   `yaml:"prefix,omitempty" json:"prefix,omitempty"`
	Region       string   `yaml:"region,omitempty" json:"region,omitempty"`
	AllowedPaths []string `yaml:"allowedPaths,omitempty" json:"allowedPaths,omitempty"`
	Endpoint     string   `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`

	// motherduck
	Database string `yaml:"database,omitempty" json:"database,omitempty"`
	TokenRef string `yaml:"tokenRef,omitempty" json:"tokenRef,omitempty"`

	// common
	Readonly    bool        `yaml:"readonly" json:"readonly"`
	Schemas     []string    `yaml:"schemas,omitempty" json:"schemas,omitempty"`
	Tables      []string    `yaml:"tables,omitempty" json:"tables,omitempty"`
	AttachMode  AttachMode  `yaml:"attachMode,omitempty" json:"attachMode,omitempty"`
	HealthCheck *HealthSpec `yaml:"healthCheck,omitempty" json:"healthCheck,omitempty"`
}

// DataSourceType names the driver that backs the DataSource.
type DataSourceType string

const (
	DSPostgres   DataSourceType = "postgres"
	DSMySQL      DataSourceType = "mysql"
	DSSQLite     DataSourceType = "sqlite"
	DSS3Parquet  DataSourceType = "s3_parquet"
	DSDuckDBFile DataSourceType = "duckdb_file"
	DSMotherDuck DataSourceType = "motherduck"
)

// AttachMode controls DuckDB ATTACH behavior.
type AttachMode string

const (
	AttachReadonly  AttachMode = "readonly"
	AttachReadwrite AttachMode = "readwrite" // v2
)

// HealthSpec describes a per-data-source liveness probe.
type HealthSpec struct {
	Query    string   `yaml:"query" json:"query"`
	Interval Duration `yaml:"interval" json:"interval"`
}

// GetTypeMeta implements Object.
func (d *DataSource) GetTypeMeta() TypeMeta { return d.TypeMeta }

// GetObjectMeta implements Object.
func (d *DataSource) GetObjectMeta() ObjectMeta { return d.Metadata }

// GetKind implements Object.
func (d *DataSource) GetKind() Kind { return KindDataSource }
