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

	// s3
	Bucket       string   `yaml:"bucket,omitempty" json:"bucket,omitempty"`
	Prefix       string   `yaml:"prefix,omitempty" json:"prefix,omitempty"`
	Region       string   `yaml:"region,omitempty" json:"region,omitempty"`
	AllowedPaths []string `yaml:"allowedPaths,omitempty" json:"allowedPaths,omitempty"`
	Endpoint     string   `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`

	// http
	URLs       []string `yaml:"urls,omitempty" json:"urls,omitempty"`
	HeadersRef string   `yaml:"headersRef,omitempty" json:"headersRef,omitempty"`

	// s3 / http: file format for the exposed views — one of "parquet",
	// "csv", "json", "xlsx". Empty means "infer per path from the file
	// extension"; loading fails when a path's format cannot be inferred.
	Format FileFormat `yaml:"format,omitempty" json:"format,omitempty"`

	// motherduck
	Database string `yaml:"database,omitempty" json:"database,omitempty"`
	TokenRef string `yaml:"tokenRef,omitempty" json:"tokenRef,omitempty"`

	// common
	// Readonly, Schemas, Tables, AttachMode and HealthCheck have no
	// effect: every data source is attached read-only and exposes all its
	// tables. The decoder refuses a value that asks for anything else.
	Readonly    *bool       `yaml:"readonly,omitempty" json:"readonly,omitempty"`
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
	DSS3         DataSourceType = "s3"
	DSHTTP       DataSourceType = "http"
	DSDuckDBFile DataSourceType = "duckdb_file"
	DSMotherDuck DataSourceType = "motherduck"
)

// FileFormat selects the DuckDB reader backing a file-based view.
type FileFormat string

const (
	FileFormatParquet FileFormat = "parquet"
	FileFormatCSV     FileFormat = "csv"
	FileFormatJSON    FileFormat = "json"
	FileFormatXLSX    FileFormat = "xlsx"
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
