// SPDX-License-Identifier: AGPL-3.0-or-later

// Package mcp exposes the data-plane as a Model Context Protocol server.
// Four tools are registered: execute_sql, list_catalogs, list_tables, and
// describe_table. Two transport modes are supported: stdio (default for
// local agents) and Streamable HTTP (for hosted AI platforms).
//
// The MCP server shares the queryservice.Service instance with the REST
// transport, so every tool call runs through the same policy / rewrite /
// audit pipeline.
package mcp
