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
			name: "server-only TLS is valid",
			mutate: func(c *ServerConfig) {
				c.REST.TLS = &TLSConfig{CertFile: "cert.pem", KeyFile: "key.pem"}
			},
		},
		{
			name: "rest mTLS fields rejected",
			mutate: func(c *ServerConfig) {
				c.REST.TLS = &TLSConfig{CertFile: "cert.pem", KeyFile: "key.pem", ClientCA: "ca.pem"}
			},
			wantErr: []string{"rest.tls.clientCA"},
		},
		{
			name: "rest clientAuth rejected",
			mutate: func(c *ServerConfig) {
				c.REST.TLS = &TLSConfig{ClientAuth: "require"}
			},
			wantErr: []string{"rest.tls.clientCA/clientAuth"},
		},
		{
			name: "admin.tls rejected",
			mutate: func(c *ServerConfig) {
				c.Admin.TLS = &TLSConfig{CertFile: "cert.pem", KeyFile: "key.pem"}
			},
			wantErr: []string{"admin.tls"},
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
			name: "multiple violations all reported",
			mutate: func(c *ServerConfig) {
				c.Admin.TLS = &TLSConfig{}
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
