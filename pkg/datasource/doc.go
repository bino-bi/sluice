// SPDX-License-Identifier: Apache-2.0

// Package datasource defines the DataSource driver interface, schema types
// (Schema, Table, Column), and the registry that third-party drivers plug
// into. It is credential-agnostic: secret resolution is supplied via the
// SecretResolver interface and implemented in internal/secrets.
package datasource
