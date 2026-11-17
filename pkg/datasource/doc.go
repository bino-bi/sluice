// SPDX-License-Identifier: Apache-2.0

// Package datasource defines the DataSource driver interface, schema types
// (Schema, Table, Column), and the registry of the built-in drivers. A new
// driver needs a change in this repository. It is credential-agnostic: secret resolution is supplied via the
// SecretResolver interface and implemented in internal/secrets.
package datasource
