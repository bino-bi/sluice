// SPDX-License-Identifier: Apache-2.0

package apitypes

// AuditSink configures where audit records are written. Only the file
// sink is honoured from manifests; syslog and s3 sinks exist but are
// configured via the audit.* server-config block, and postgres/otlp are
// declared here so policy files targeting v1 parse cleanly.
type AuditSink struct {
	TypeMeta `yaml:",inline" json:",inline"`
	Metadata ObjectMeta    `yaml:"metadata" json:"metadata"`
	Spec     AuditSinkSpec `yaml:"spec" json:"spec"`
}

// AuditSinkSpec carries per-sink configuration.
type AuditSinkSpec struct {
	Type AuditSinkType `yaml:"type" json:"type"`

	// file
	Path         string `yaml:"path,omitempty" json:"path,omitempty"`
	RotateDaily  bool   `yaml:"rotateDaily,omitempty" json:"rotateDaily,omitempty"`
	RotateSizeMB int    `yaml:"rotateSizeMB,omitempty" json:"rotateSizeMB,omitempty"`

	// s3
	Bucket         string     `yaml:"bucket,omitempty" json:"bucket,omitempty"`
	Prefix         string     `yaml:"prefix,omitempty" json:"prefix,omitempty"`
	Region         string     `yaml:"region,omitempty" json:"region,omitempty"`
	ObjectLock     S3LockMode `yaml:"objectLock,omitempty" json:"objectLock,omitempty"`
	RetentionDays  int        `yaml:"retentionDays,omitempty" json:"retentionDays,omitempty"`
	CredentialsRef string     `yaml:"credentialsRef,omitempty" json:"credentialsRef,omitempty"`

	// postgres
	Connection string `yaml:"connection,omitempty" json:"connection,omitempty"`
	Table      string `yaml:"table,omitempty" json:"table,omitempty"`

	// syslog
	Network  string `yaml:"network,omitempty" json:"network,omitempty"`
	Address  string `yaml:"address,omitempty" json:"address,omitempty"`
	Facility string `yaml:"facility,omitempty" json:"facility,omitempty"`

	// otlp
	Endpoint string `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
}

// AuditSinkType names an audit sink backend.
type AuditSinkType string

const (
	AuditFile     AuditSinkType = "file"     // MVP
	AuditS3       AuditSinkType = "s3"       // v1
	AuditPostgres AuditSinkType = "postgres" // v1
	AuditSyslog   AuditSinkType = "syslog"   // v1
	AuditOTLP     AuditSinkType = "otlp"     // v1
)

// S3LockMode selects the S3 Object Lock retention mode.
type S3LockMode string

const (
	S3LockCompliance S3LockMode = "compliance"
	S3LockGovernance S3LockMode = "governance"
)

// GetTypeMeta implements Object.
func (a *AuditSink) GetTypeMeta() TypeMeta { return a.TypeMeta }

// GetObjectMeta implements Object.
func (a *AuditSink) GetObjectMeta() ObjectMeta { return a.Metadata }

// GetKind implements Object.
func (a *AuditSink) GetKind() Kind { return KindAuditSink }
