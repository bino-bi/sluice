// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"strings"
	"testing"
)

func TestServerConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*ServerConfig)
		wantErr []string // substrings; empty = valid
	}{
		{
			name:   "defaults are valid",
			mutate: func(*ServerConfig) {},
		},
		{
			name:    "unknown policy engine",
			mutate:  func(c *ServerConfig) { c.Policies.Engine = "rego" },
			wantErr: []string{`policies.engine: unknown engine "rego"`},
		},
		{
			name:    "opa engine without a module directory",
			mutate:  func(c *ServerConfig) { c.Policies.Engine = "opa" },
			wantErr: []string{"policies.opa.moduleDir: required"},
		},
		{
			name: "opa engine with a module directory",
			mutate: func(c *ServerConfig) {
				c.Policies.Engine = "opa"
				c.Policies.OPA.ModuleDir = "./rego"
			},
		},
		{
			name: "unknown composite member",
			mutate: func(c *ServerConfig) {
				c.Policies.Engine = "composite"
				c.Policies.Composite.Members = []string{"yaml", "spicedb"}
			},
			wantErr: []string{`policies.composite.members[1]: unknown member "spicedb"`},
		},
		{
			name: "composite with opa and no module directory",
			mutate: func(c *ServerConfig) {
				c.Policies.Engine = "composite"
				c.Policies.Composite.Members = []string{"yaml", "opa"}
			},
			wantErr: []string{"policies.opa.moduleDir: required"},
		},
		{
			name: "composite of yaml and rebac",
			mutate: func(c *ServerConfig) {
				c.Policies.Engine = "composite"
				c.Policies.Composite.Members = []string{"yaml", "rebac"}
			},
		},
		{
			name: "members are not read for the yaml engine",
			mutate: func(c *ServerConfig) {
				c.Policies.Composite.Members = []string{"spicedb"}
			},
		},
		{
			name:    "datasources.directory is not read",
			mutate:  func(c *ServerConfig) { c.DataSources.Directory = "./datasources.d" },
			wantErr: []string{"datasources.directory: not read"},
		},
		{
			name:   "admin.token as plain text",
			mutate: func(c *ServerConfig) { c.Admin.Token = "s3cret" },
		},
		{
			name:   "admin.token as a secret reference",
			mutate: func(c *ServerConfig) { c.Admin.Token = "secret://env/ADMIN_TOKEN" },
		},
		{
			name:    "admin.token with a reference in another case that cannot resolve",
			mutate:  func(c *ServerConfig) { c.Admin.Token = " Secret://vault/admin" },
			wantErr: []string{"admin.token:"},
		},
		{
			name:    "admin.token with a reference that cannot resolve",
			mutate:  func(c *ServerConfig) { c.Admin.Token = "secret://vault/admin" },
			wantErr: []string{"admin.token:"},
		},
		{
			name: "server-only TLS is valid",
			mutate: func(c *ServerConfig) {
				c.REST.TLS = &TLSConfig{CertFile: "cert.pem", KeyFile: "key.pem"}
			},
		},
		{
			name: "rest mTLS via clientCA is valid",
			mutate: func(c *ServerConfig) {
				c.REST.TLS = &TLSConfig{CertFile: "cert.pem", KeyFile: "key.pem", ClientCA: "ca.pem"}
			},
		},
		{
			name: "rest explicit clientAuth with clientCA is valid",
			mutate: func(c *ServerConfig) {
				c.REST.TLS = &TLSConfig{
					CertFile: "cert.pem", KeyFile: "key.pem",
					ClientCA: "ca.pem", ClientAuth: "require_and_verify",
				}
			},
		},
		{
			name: "unknown clientAuth mode rejected",
			mutate: func(c *ServerConfig) {
				c.REST.TLS = &TLSConfig{
					CertFile: "cert.pem", KeyFile: "key.pem",
					ClientCA: "ca.pem", ClientAuth: "request",
				}
			},
			wantErr: []string{"rest.tls.clientAuth", "unknown mode"},
		},
		{
			name: "clientAuth without clientCA rejected",
			mutate: func(c *ServerConfig) {
				c.REST.TLS = &TLSConfig{
					CertFile: "cert.pem", KeyFile: "key.pem",
					ClientAuth: "require_and_verify",
				}
			},
			wantErr: []string{"rest.tls.clientAuth", "requires rest.tls.clientCA"},
		},
		{
			name: "partial tls block rejected",
			mutate: func(c *ServerConfig) {
				c.REST.TLS = &TLSConfig{CertFile: "cert.pem"}
			},
			wantErr: []string{"rest.tls", "certFile and keyFile"},
		},
		{
			name: "admin.tls with cert and key is valid",
			mutate: func(c *ServerConfig) {
				c.Admin.TLS = &TLSConfig{CertFile: "cert.pem", KeyFile: "key.pem"}
			},
		},
		{
			name: "admin.tls missing keyFile rejected",
			mutate: func(c *ServerConfig) {
				c.Admin.TLS = &TLSConfig{CertFile: "cert.pem"}
			},
			wantErr: []string{"admin.tls", "certFile and keyFile"},
		},
		{
			name:    "audit.chainKeyRef must be a secret reference",
			mutate:  func(c *ServerConfig) { c.Audit.ChainKeyRef = "plain-text-key" },
			wantErr: []string{"audit.chainKeyRef"},
		},
		{
			name:   "audit.chainKeyRef as a secret reference",
			mutate: func(c *ServerConfig) { c.Audit.ChainKeyRef = "secret://env/SLUICE_AUDIT_KEY" },
		},
		{
			name:    "negative result size cap rejected",
			mutate:  func(c *ServerConfig) { c.Limits.MaxResultBytes = -1 },
			wantErr: []string{"limits.maxResultBytes"},
		},
		{
			name:   "result size cap turned off",
			mutate: func(c *ServerConfig) { c.Limits.MaxResultBytes = 0 },
		},
		{
			name: "mcp.tls with cert and key is valid",
			mutate: func(c *ServerConfig) {
				c.MCP.TLS = &TLSConfig{CertFile: "cert.pem", KeyFile: "key.pem"}
			},
		},
		{
			name: "mcp.tls missing keyFile rejected",
			mutate: func(c *ServerConfig) {
				c.MCP.TLS = &TLSConfig{CertFile: "cert.pem"}
			},
			wantErr: []string{"mcp.tls", "certFile and keyFile"},
		},
		{
			name: "mcp over http without an address rejected",
			mutate: func(c *ServerConfig) {
				c.MCP.Enabled = true
				c.MCP.Transport = "streamable_http"
			},
			wantErr: []string{"mcp.listen"},
		},
		{
			name: "unknown mcp transport rejected",
			mutate: func(c *ServerConfig) {
				c.MCP.Enabled = true
				c.MCP.Transport = "sse"
				c.MCP.Listen = ":8081"
			},
			wantErr: []string{"mcp.transport", `"sse"`},
		},
		{
			name: "mcp over http with an address is valid",
			mutate: func(c *ServerConfig) {
				c.MCP.Enabled = true
				c.MCP.Transport = "http"
				c.MCP.Listen = ":8081"
			},
		},
		{
			name: "tracing enabled without endpoint rejected",
			mutate: func(c *ServerConfig) {
				c.Tracing.Enabled = true
			},
			wantErr: []string{"tracing.endpoint"},
		},
		{
			name: "tracing bad protocol rejected",
			mutate: func(c *ServerConfig) {
				c.Tracing.Enabled = true
				c.Tracing.Endpoint = "otel:4317"
				c.Tracing.Protocol = "udp"
			},
			wantErr: []string{"tracing.protocol", "unknown protocol"},
		},
		{
			name: "tracing sampleRatio out of range rejected",
			mutate: func(c *ServerConfig) {
				c.Tracing.SampleRatio = 1.5
			},
			wantErr: []string{"tracing.sampleRatio"},
		},
		{
			name: "tracing enabled with endpoint valid",
			mutate: func(c *ServerConfig) {
				c.Tracing.Enabled = true
				c.Tracing.Endpoint = "otel:4317"
				c.Tracing.Insecure = true
			},
		},
		{
			name: "datasources.reload rejected",
			mutate: func(c *ServerConfig) {
				c.DataSources.Reload = true
			},
			wantErr: []string{"datasources.reload"},
		},
		{
			name: "vault genesis ref rejected",
			mutate: func(c *ServerConfig) {
				c.Audit.File = &FileSinkConfig{Path: "/tmp/audit", Genesis: "secret://vault/x"}
			},
			wantErr: []string{"audit.file.genesis", "parsed but unimplemented"},
		},
		{
			name: "aws-sm pepper ref rejected",
			mutate: func(c *ServerConfig) {
				c.Identity.APIKeyPepper = "secret://aws-sm/pepper"
			},
			wantErr: []string{"identity.apiKeyPepper"},
		},
		{
			name: "gcp-sm webhook headers ref rejected",
			mutate: func(c *ServerConfig) {
				c.Approval.Webhooks = []ApprovalWebhook{{URL: "https://x", HeadersRef: "secret://gcp-sm/h"}}
			},
			wantErr: []string{"approval.webhooks[0].headersRef"},
		},
		{
			name: "env refs are valid",
			mutate: func(c *ServerConfig) {
				c.Audit.File = &FileSinkConfig{Path: "/tmp/audit", Genesis: "secret://env/GENESIS"}
				c.Identity.APIKeyPepper = "secret://env/PEPPER"
			},
		},
		{
			name: "syslog sink without address rejected",
			mutate: func(c *ServerConfig) {
				c.Audit.Syslog = &SyslogSinkConfig{Network: "tcp"}
			},
			wantErr: []string{"audit.syslog.address"},
		},
		{
			name: "syslog sink with bad network rejected",
			mutate: func(c *ServerConfig) {
				c.Audit.Syslog = &SyslogSinkConfig{Address: "localhost:514", Network: "sctp"}
			},
			wantErr: []string{"audit.syslog.network"},
		},
		{
			name: "syslog sink valid",
			mutate: func(c *ServerConfig) {
				c.Audit.Syslog = &SyslogSinkConfig{Address: "localhost:514"}
			},
		},
		{
			name: "s3 sink without bucket rejected",
			mutate: func(c *ServerConfig) {
				c.Audit.S3 = &S3SinkConfig{}
			},
			wantErr: []string{"audit.s3.bucket"},
		},
		{
			name: "s3 objectLock without retention rejected",
			mutate: func(c *ServerConfig) {
				c.Audit.S3 = &S3SinkConfig{Bucket: "b", ObjectLock: "compliance"}
			},
			wantErr: []string{"audit.s3.retentionDays"},
		},
		{
			name: "s3 bad objectLock mode rejected",
			mutate: func(c *ServerConfig) {
				c.Audit.S3 = &S3SinkConfig{Bucket: "b", ObjectLock: "worm", RetentionDays: 30}
			},
			wantErr: []string{"audit.s3.objectLock"},
		},
		{
			name: "s3 vault credentialsRef rejected",
			mutate: func(c *ServerConfig) {
				c.Audit.S3 = &S3SinkConfig{Bucket: "b", CredentialsRef: "secret://vault/creds"}
			},
			wantErr: []string{"audit.s3.credentialsRef"},
		},
		{
			name: "s3 sink valid with lock",
			mutate: func(c *ServerConfig) {
				c.Audit.S3 = &S3SinkConfig{Bucket: "b", ObjectLock: "governance", RetentionDays: 90}
			},
		},
		{
			name: "mcp stdio without credential rejected",
			mutate: func(c *ServerConfig) {
				c.MCP.Enabled = true
			},
			wantErr: []string{"mcp.enabled with transport=stdio", "mcp.tokenRef"},
		},
		{
			name: "mcp stdio with tokenRef is valid",
			mutate: func(c *ServerConfig) {
				c.MCP.Enabled = true
				c.MCP.Transport = "stdio"
				c.MCP.TokenRef = "secret://env/MCP_TOKEN"
			},
		},
		{
			name: "mcp stdio with allowAnonymous is valid",
			mutate: func(c *ServerConfig) {
				c.MCP.Enabled = true
				c.MCP.AllowAnonymous = true
			},
		},
		{
			name: "mcp streamable_http without credential is valid",
			mutate: func(c *ServerConfig) {
				c.MCP.Enabled = true
				c.MCP.Transport = "streamable_http"
				c.MCP.Listen = ":8081"
			},
		},
		{
			name: "mcp disabled needs nothing",
			mutate: func(c *ServerConfig) {
				c.MCP.Transport = "stdio"
			},
		},
		{
			name: "mcp vault tokenRef rejected",
			mutate: func(c *ServerConfig) {
				c.MCP.Enabled = true
				c.MCP.TokenRef = "secret://vault/token"
			},
			wantErr: []string{"mcp.tokenRef", "parsed but unimplemented"},
		},
		{
			name:   "logged error cut switched off",
			mutate: func(c *ServerConfig) { c.Logging.MaxErrorBytes = 0 },
		},
		{
			name:    "negative logged error cut",
			mutate:  func(c *ServerConfig) { c.Logging.MaxErrorBytes = -1 },
			wantErr: []string{"logging.maxErrorBytes", "negative"},
		},
		{
			name: "multiple violations all reported",
			mutate: func(c *ServerConfig) {
				c.Admin.TLS = &TLSConfig{CertFile: "cert.pem"} // missing keyFile
				c.DataSources.Reload = true
				c.Identity.APIKeyPepper = "secret://vault/p"
			},
			wantErr: []string{"admin.tls", "datasources.reload", "identity.apiKeyPepper"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultServerConfig()
			tc.mutate(&cfg)
			err := cfg.Validate()
			if len(tc.wantErr) == 0 {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected validation error")
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error missing %q:\n%v", want, err)
				}
			}
		})
	}
}
