// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"fmt"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// toolErrorResult converts an error into an MCP CallToolResult with
// IsError=true. Every APIError field we keep is safe to expose (the
// Message has already been scrubbed by pkg/errors).
func toolErrorResult(err error) *sdkmcp.CallToolResult {
	ae := pkgerr.FromError(err)
	if ae == nil {
		ae = pkgerr.New(pkgerr.CodeInternal)
	}
	msg := fmt.Sprintf("%s: %s", ae.Code, ae.Message)
	if ae.Policy != "" {
		msg += fmt.Sprintf(" (policy=%s)", ae.Policy)
	}
	if ae.QueryID != "" {
		msg += fmt.Sprintf(" (query_id=%s)", ae.QueryID)
	}
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: msg},
		},
		IsError: true,
	}
}

// renderTextTable produces a human-friendly ASCII table for the MCP
// textual content channel. Structured content (ExecuteSQLOutput) carries
// the same data for agents that prefer JSON.
func renderTextTable(out ExecuteSQLOutput, maxRows int) string {
	if len(out.Columns) == 0 {
		return fmt.Sprintf("(0 columns, query_id=%s)", out.QueryID)
	}
	var b strings.Builder
	b.WriteString(strings.Join(out.Columns, " | "))
	b.WriteByte('\n')
	b.WriteString(strings.Repeat("-", len(out.Columns)*8))
	b.WriteByte('\n')
	n := min(len(out.Rows), maxRows)
	for i := range n {
		row := out.Rows[i]
		parts := make([]string, len(row))
		for j, v := range row {
			parts[j] = fmt.Sprintf("%v", v)
		}
		b.WriteString(strings.Join(parts, " | "))
		b.WriteByte('\n')
	}
	if len(out.Rows) > n {
		_, _ = fmt.Fprintf(&b, "... (%d more rows)\n", len(out.Rows)-n)
	}
	_, _ = fmt.Fprintf(&b, "row_count=%d truncated=%v query_id=%s\n",
		out.RowCount, out.Truncated, out.QueryID)
	return b.String()
}
