// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"errors"
	"fmt"

	"github.com/bino-bi/sluice/internal/secrets"
)

// Validate rejects configuration that parses but is not enforced by this
// build. Fail-closed: an operator must never believe a control is active
// when it is not. Guards are removed as the backing features land.
//
// Called by `sluice config validate` (exit 3) and by the serve/mcp boot
// path (refuse to start).
func (c *ServerConfig) Validate() error {
	var errs []error
	errs = append(errs, validateTLSBlock("rest.tls", c.REST.TLS)...)
	errs = append(errs, validateTLSBlock("admin.tls", c.Admin.TLS)...)
	if c.Tracing.Enabled && c.Tracing.Endpoint == "" {
		errs = append(errs, errors.New("tracing.endpoint: required when tracing is enabled"))
	}
	switch c.Tracing.Protocol {
	case "", "grpc", "http":
	default:
		errs = append(errs, fmt.Errorf("tracing.protocol: unknown protocol %q (grpc, http)", c.Tracing.Protocol))
	}
	if c.Tracing.SampleRatio < 0 || c.Tracing.SampleRatio > 1 {
		errs = append(errs, fmt.Errorf("tracing.sampleRatio: %v out of range [0, 1]", c.Tracing.SampleRatio))
	}
	if c.DataSources.Reload {
		errs = append(errs, errors.New(
			"datasources.reload: parsed but unimplemented — DataSource manifest changes require a restart (docs/operations/server-config.md)"))
	}
	if c.Audit.File != nil && c.Audit.File.Genesis != "" {
		if err := checkSecretRef("audit.file.genesis", c.Audit.File.Genesis); err != nil {
			errs = append(errs, err)
		}
	}
	if s := c.Audit.Syslog; s != nil {
		if s.Address == "" {
			errs = append(errs, errors.New("audit.syslog.address: required when the syslog sink is configured"))
		}
		switch s.Network {
		case "", "udp", "tcp", "unix", "unixgram":
		default:
			errs = append(errs, fmt.Errorf("audit.syslog.network: unknown network %q (udp, tcp, unix, unixgram)", s.Network))
		}
	}
	if s := c.Audit.S3; s != nil {
		if s.Bucket == "" {
			errs = append(errs, errors.New("audit.s3.bucket: required when the s3 sink is configured"))
		}
		switch s.ObjectLock {
		case "", "governance", "compliance":
		default:
			errs = append(errs, fmt.Errorf("audit.s3.objectLock: unknown mode %q (governance, compliance)", s.ObjectLock))
		}
		if s.ObjectLock != "" && s.RetentionDays <= 0 {
			errs = append(errs, errors.New("audit.s3.retentionDays: must be > 0 when objectLock is set"))
		}
		if s.CredentialsRef != "" {
			if err := checkSecretRef("audit.s3.credentialsRef", s.CredentialsRef); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if c.Identity.APIKeyPepper != "" {
		if err := checkSecretRef("identity.apiKeyPepper", c.Identity.APIKeyPepper); err != nil {
			errs = append(errs, err)
		}
	}
	for i, wh := range c.Approval.Webhooks {
		if wh.HeadersRef != "" {
			if err := checkSecretRef(fmt.Sprintf("approval.webhooks[%d].headersRef", i), wh.HeadersRef); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if c.MCP.TokenRef != "" {
		if err := checkSecretRef("mcp.tokenRef", c.MCP.TokenRef); err != nil {
			errs = append(errs, err)
		}
	}
	if c.MCP.APIKeyRef != "" {
		if err := checkSecretRef("mcp.apiKeyRef", c.MCP.APIKeyRef); err != nil {
			errs = append(errs, err)
		}
	}
	// The serve-embedded stdio transport pins one identity for every tool
	// call; without a credential it would silently run anonymous, so an
	// anonymous run must be an explicit choice. "" and "stdio" are the
	// stdio spellings; the http aliases are normalised by the transport.
	if c.MCP.Enabled && (c.MCP.Transport == "" || c.MCP.Transport == "stdio") &&
		c.MCP.TokenRef == "" && c.MCP.APIKeyRef == "" && !c.MCP.AllowAnonymous {
		errs = append(errs, errors.New(
			"mcp.enabled with transport=stdio requires a pinned credential for the serve-embedded transport: set mcp.tokenRef or mcp.apiKeyRef, or mcp.allowAnonymous: true to run default-denied anonymous (docs/reference/mcp.md)"))
	}
	return errors.Join(errs...)
}

// checkSecretRef parses ref so unimplemented providers (vault/aws-sm/
// gcp-sm) and malformed URIs fail at load, prefixed with the field path.
func checkSecretRef(field, ref string) error {
	if _, err := secrets.Parse(ref); err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	return nil
}

// validateTLSBlock enforces the structure of a tls block: certFile and
// keyFile must both be set (a partial block would silently serve plain
// HTTP — fail-open), clientAuth accepts only the supported
// require_and_verify mode, and clientAuth without a clientCA is
// unenforceable. File readability is checked at listener start, not here:
// Validate stays I/O-free.
func validateTLSBlock(field string, t *TLSConfig) []error {
	if t == nil {
		return nil
	}
	var errs []error
	if t.CertFile == "" || t.KeyFile == "" {
		errs = append(errs, fmt.Errorf("%s: certFile and keyFile are both required when a tls block is set", field))
	}
	switch t.ClientAuth {
	case "", "require_and_verify":
	default:
		errs = append(errs, fmt.Errorf("%s.clientAuth: unknown mode %q (only require_and_verify is supported; a set clientCA implies it)", field, t.ClientAuth))
	}
	if t.ClientAuth != "" && t.ClientCA == "" {
		errs = append(errs, fmt.Errorf("%s.clientAuth: requires %s.clientCA", field, field))
	}
	return errs
}
