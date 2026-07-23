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
	if c.REST.TLS != nil && (c.REST.TLS.ClientCA != "" || c.REST.TLS.ClientAuth != "") {
		errs = append(errs, errors.New(
			"rest.tls.clientCA/clientAuth: parsed but unimplemented — mTLS lands in a later release; terminate mTLS at a proxy for now (docs/security/hardening.md)"))
	}
	if c.Admin.TLS != nil {
		errs = append(errs, errors.New(
			"admin.tls: parsed but unimplemented — the admin listener serves plain HTTP; terminate TLS in front of it (docs/reference/admin-api.md)"))
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
