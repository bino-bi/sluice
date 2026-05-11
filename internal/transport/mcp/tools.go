// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"fmt"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/queryservice"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// registerTools wires the four MVP tools onto the underlying MCP server.
func (s *Server) registerTools() {
	sdkmcp.AddTool(s.mcp,
		&sdkmcp.Tool{
			Name:        "execute_sql",
			Description: "Execute a SELECT against attached catalogs, subject to Sluice policies.",
		},
		s.toolExecuteSQL,
	)
	sdkmcp.AddTool(s.mcp,
		&sdkmcp.Tool{
			Name:        "list_catalogs",
			Description: "List attached data sources (catalogs).",
		},
		s.toolListCatalogs,
	)
	sdkmcp.AddTool(s.mcp,
		&sdkmcp.Tool{
			Name:        "list_tables",
			Description: "List tables in a catalog/schema.",
		},
		s.toolListTables,
	)
	sdkmcp.AddTool(s.mcp,
		&sdkmcp.Tool{
			Name:        "describe_table",
			Description: "Return the column list for a table, e.g. pg.public.orders.",
		},
		s.toolDescribeTable,
	)
}

// ExecuteSQLArgs is the typed argument schema for execute_sql. The SDK
// generates the JSON schema from this struct.
type ExecuteSQLArgs struct {
	SQL      string `json:"sql" jsonschema:"SQL SELECT statement"`
	RowLimit int64  `json:"row_limit,omitempty" jsonschema:"maximum rows to return (1..100000)"`
}

// ExecuteSQLOutput is the structured output for execute_sql.
type ExecuteSQLOutput struct {
	QueryID   string   `json:"query_id"`
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	RowCount  int64    `json:"row_count"`
	Truncated bool     `json:"truncated"`
}

func (s *Server) toolExecuteSQL(ctx context.Context, _ *sdkmcp.CallToolRequest, in ExecuteSQLArgs) (*sdkmcp.CallToolResult, ExecuteSQLOutput, error) {
	user, _ := userFrom(ctx)
	qreq := queryservice.QueryRequest{
		SQL:     in.SQL,
		MaxRows: in.RowLimit,
		Format:  executor.FormatJSON,
		User:    user,
		Origin:  queryservice.OriginMCP,
	}
	res, err := s.deps.Service.Execute(ctx, qreq)
	if err != nil {
		return toolErrorResult(err), ExecuteSQLOutput{}, nil
	}
	defer func() { _ = res.Rows.Close() }()

	out := ExecuteSQLOutput{
		QueryID: res.QueryID,
		Columns: columnNames(res.Columns),
	}
	scan := make([]any, len(res.Columns))
	ptrs := make([]any, len(res.Columns))
	for i := range scan {
		ptrs[i] = &scan[i]
	}
	for res.Rows.Next() {
		if err := res.Rows.Scan(ptrs...); err != nil {
			return toolErrorResult(err), out, nil
		}
		row := make([]any, len(scan))
		for i, v := range scan {
			row[i] = jsonSafe(v)
		}
		out.Rows = append(out.Rows, row)
	}
	if err := res.Rows.Err(); err != nil {
		return toolErrorResult(err), out, nil
	}
	if res.RowCount != nil {
		out.RowCount = *res.RowCount
	}
	out.Truncated = res.Truncated

	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: renderTextTable(out, 100)},
		},
	}, out, nil
}

// ListCatalogsArgs is empty — list_catalogs takes no parameters.
type ListCatalogsArgs struct{}

// ListCatalogsOutput carries the catalog list.
type ListCatalogsOutput struct {
	Catalogs []CatalogEntry `json:"catalogs"`
}

// CatalogEntry is one line of the catalog list.
type CatalogEntry struct {
	Name    string `json:"name"`
	Type    string `json:"type,omitempty"`
	Healthy bool   `json:"healthy"`
}

func (s *Server) toolListCatalogs(ctx context.Context, _ *sdkmcp.CallToolRequest, _ ListCatalogsArgs) (*sdkmcp.CallToolResult, ListCatalogsOutput, error) {
	user, _ := userFrom(ctx)
	if s.deps.Catalogs == nil {
		return toolErrorResult(pkgerr.New(pkgerr.CodeInternal).WithMessage("catalog lister not configured")),
			ListCatalogsOutput{}, nil
	}
	catalogs, err := s.deps.Service.ListCatalogs(ctx, s.deps.Catalogs, user)
	if err != nil {
		return toolErrorResult(err), ListCatalogsOutput{}, nil
	}
	out := ListCatalogsOutput{
		Catalogs: make([]CatalogEntry, 0, len(catalogs)),
	}
	for _, c := range catalogs {
		out.Catalogs = append(out.Catalogs, CatalogEntry{
			Name: c.Name, Type: c.Type, Healthy: c.Healthy,
		})
	}
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: fmt.Sprintf("%d catalog(s)", len(out.Catalogs))},
		},
	}, out, nil
}

// ListTablesArgs identifies a catalog (+ optional schema).
type ListTablesArgs struct {
	Catalog string `json:"catalog" jsonschema:"catalog name"`
	Schema  string `json:"schema,omitempty" jsonschema:"schema within the catalog (optional)"`
}

// ListTablesOutput carries the table list.
type ListTablesOutput struct {
	Tables []string `json:"tables"`
}

func (s *Server) toolListTables(ctx context.Context, _ *sdkmcp.CallToolRequest, in ListTablesArgs) (*sdkmcp.CallToolResult, ListTablesOutput, error) {
	tables, err := s.deps.Service.ListTables(ctx, in.Catalog, in.Schema)
	if err != nil {
		return toolErrorResult(err), ListTablesOutput{}, nil
	}
	out := ListTablesOutput{Tables: make([]string, 0, len(tables))}
	for _, t := range tables {
		out.Tables = append(out.Tables, qualified(t))
	}
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: fmt.Sprintf("%d table(s)", len(out.Tables))},
		},
	}, out, nil
}

// DescribeTableArgs selects a table by fully-qualified name.
type DescribeTableArgs struct {
	Table string `json:"table" jsonschema:"fully-qualified name (catalog.schema.table)"`
}

// DescribeTableOutput carries the column list.
type DescribeTableOutput struct {
	Table   string           `json:"table"`
	Columns []ColumnMetaInfo `json:"columns"`
}

// ColumnMetaInfo is one row in the describe_table output.
type ColumnMetaInfo struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
}

func (s *Server) toolDescribeTable(ctx context.Context, _ *sdkmcp.CallToolRequest, in DescribeTableArgs) (*sdkmcp.CallToolResult, DescribeTableOutput, error) {
	ref, err := parseTableRef(in.Table)
	if err != nil {
		return toolErrorResult(pkgerr.Newf(pkgerr.CodeSyntax, "%s", err.Error())),
			DescribeTableOutput{}, nil
	}
	entry, err := s.deps.Service.DescribeTable(ctx, ref)
	if err != nil {
		return toolErrorResult(err), DescribeTableOutput{}, nil
	}
	out := DescribeTableOutput{
		Table:   in.Table,
		Columns: make([]ColumnMetaInfo, 0, len(entry.Columns)),
	}
	for _, c := range entry.Columns {
		out.Columns = append(out.Columns, ColumnMetaInfo{
			Name:     c.Name,
			Type:     c.SQLType,
			Nullable: c.Nullable,
		})
	}
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: fmt.Sprintf("%d column(s)", len(out.Columns))},
		},
	}, out, nil
}

// parseTableRef splits "catalog.schema.table" into components. Two-part
// names are accepted as catalog.table with an empty schema.
func parseTableRef(s string) (parser.TableRef, error) {
	parts := strings.Split(s, ".")
	switch len(parts) {
	case 3:
		return parser.TableRef{Catalog: parts[0], Schema: parts[1], Table: parts[2]}, nil
	case 2:
		return parser.TableRef{Catalog: parts[0], Table: parts[1]}, nil
	case 1:
		return parser.TableRef{Table: parts[0]}, nil
	default:
		return parser.TableRef{}, fmt.Errorf("expected catalog[.schema].table, got %q", s)
	}
}

func qualified(t parser.TableRef) string {
	if t.Schema == "" {
		return fmt.Sprintf("%s.%s", t.Catalog, t.Table)
	}
	return fmt.Sprintf("%s.%s.%s", t.Catalog, t.Schema, t.Table)
}

func columnNames(cols []executor.ColumnInfo) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Name
	}
	return out
}

// jsonSafe converts database/sql-scanned values into JSON-friendly
// shapes. []byte becomes a string; everything else passes through.
func jsonSafe(v any) any {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}
