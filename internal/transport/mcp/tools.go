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
	sdkmcp.AddTool(s.mcp,
		&sdkmcp.Tool{
			Name:        "whoami",
			Description: "Report the identity this session is authenticated as (subject, groups, claims).",
		},
		s.toolWhoAmI,
	)
	sdkmcp.AddTool(s.mcp,
		&sdkmcp.Tool{
			Name:        "explain_access",
			Description: "Explain, for the current identity, which policies apply to a table or candidate SQL — the effective decision plus row filters and column masks — WITHOUT running the query. Use this to understand what is allowed and why before calling execute_sql.",
		},
		s.toolExplainAccess,
	)
	sdkmcp.AddTool(s.mcp,
		&sdkmcp.Tool{
			Name:        "list_accessible_tables",
			Description: "List the tables the current identity is allowed to query, optionally within one catalog.",
		},
		s.toolListAccessibleTables,
	)
}

// WhoAmIArgs takes no parameters.
type WhoAmIArgs struct{}

// WhoAmIOutput describes the authenticated identity.
type WhoAmIOutput struct {
	Anonymous  bool     `json:"anonymous"`
	Subject    string   `json:"subject,omitempty"`
	Issuer     string   `json:"issuer,omitempty"`
	Email      string   `json:"email,omitempty"`
	Groups     []string `json:"groups,omitempty"`
	AuthMethod string   `json:"auth_method,omitempty"`
}

func (s *Server) toolWhoAmI(ctx context.Context, _ *sdkmcp.CallToolRequest, _ WhoAmIArgs) (*sdkmcp.CallToolResult, WhoAmIOutput, error) {
	user, ok := userFrom(ctx)
	out := WhoAmIOutput{Anonymous: !ok || user == nil}
	if user != nil {
		out.Subject = user.Subject
		out.Issuer = user.Issuer
		out.Email = user.Email
		out.Groups = append([]string(nil), user.Groups...)
		out.AuthMethod = string(user.AuthMethod)
	}
	text := "anonymous"
	if !out.Anonymous {
		text = fmt.Sprintf("subject=%s issuer=%s groups=%v", out.Subject, out.Issuer, out.Groups)
	}
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: text}},
	}, out, nil
}

// ExplainAccessArgs selects a table or a candidate SQL statement to explain.
type ExplainAccessArgs struct {
	Table string `json:"table,omitempty" jsonschema:"fully-qualified table (catalog.schema.table) to check"`
	SQL   string `json:"sql,omitempty" jsonschema:"a candidate SELECT to check instead of a single table"`
}

// PolicyRefInfo names a policy that applied.
type PolicyRefInfo struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Priority int32  `json:"priority,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// MaskRefInfo names a column mask that would apply.
type MaskRefInfo struct {
	Column   string `json:"column"`
	MaskType string `json:"mask_type"`
	Policy   string `json:"policy"`
}

// ExplainAccessOutput is the agent-facing explanation.
type ExplainAccessOutput struct {
	Subject     string          `json:"subject"`
	Resource    string          `json:"resource"`
	Decision    string          `json:"decision"`
	RowFilters  []string        `json:"row_filters,omitempty"`
	ColumnMasks []MaskRefInfo   `json:"column_masks,omitempty"`
	Matched     []PolicyRefInfo `json:"matched,omitempty"`
	Rejected    []PolicyRefInfo `json:"rejected,omitempty"`
}

func (s *Server) toolExplainAccess(ctx context.Context, _ *sdkmcp.CallToolRequest, in ExplainAccessArgs) (*sdkmcp.CallToolResult, ExplainAccessOutput, error) {
	user, _ := userFrom(ctx)
	input := queryservice.ExplainInput{User: user, Origin: queryservice.OriginMCP}
	if in.Table != "" {
		ref, err := parseTableRef(in.Table)
		if err != nil {
			return toolErrorResult(pkgerr.Newf(pkgerr.CodeSyntax, "%s", err.Error())), ExplainAccessOutput{}, nil
		}
		input.Table = ref
	}
	input.SimulatedSQL = in.SQL
	res, err := s.deps.Service.Explain(ctx, input)
	if err != nil {
		return toolErrorResult(err), ExplainAccessOutput{}, nil
	}
	out := ExplainAccessOutput{
		Subject:    res.Subject,
		Resource:   res.Resource,
		Decision:   res.Effective.Decision,
		RowFilters: append([]string(nil), res.Effective.RowFilters...),
	}
	for _, m := range res.Effective.ColumnMasks {
		out.ColumnMasks = append(out.ColumnMasks, MaskRefInfo{Column: m.Column, MaskType: string(m.MaskType), Policy: m.Policy})
	}
	for _, p := range res.Matched {
		out.Matched = append(out.Matched, PolicyRefInfo{Kind: string(p.Kind), Name: p.Name, Priority: p.Priority})
	}
	for _, p := range res.Rejected {
		out.Rejected = append(out.Rejected, PolicyRefInfo{Kind: string(p.Kind), Name: p.Name, Reason: p.Reason})
	}
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: renderExplain(out)}},
	}, out, nil
}

// ListAccessibleTablesArgs optionally restricts to one catalog.
type ListAccessibleTablesArgs struct {
	Catalog string `json:"catalog,omitempty" jsonschema:"optional catalog to restrict to"`
}

func (s *Server) toolListAccessibleTables(ctx context.Context, _ *sdkmcp.CallToolRequest, in ListAccessibleTablesArgs) (*sdkmcp.CallToolResult, ListTablesOutput, error) {
	user, _ := userFrom(ctx)
	tables, err := s.deps.Service.AccessibleTables(ctx, user, in.Catalog)
	if err != nil {
		return toolErrorResult(err), ListTablesOutput{}, nil
	}
	out := ListTablesOutput{Tables: make([]string, 0, len(tables))}
	for _, t := range tables {
		out.Tables = append(out.Tables, qualified(t))
	}
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: fmt.Sprintf("%d accessible table(s)", len(out.Tables))}},
	}, out, nil
}

// renderExplain produces a compact human/LLM-readable summary of an access
// explanation.
func renderExplain(o ExplainAccessOutput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "decision=%s subject=%s resource=%s\n", o.Decision, o.Subject, o.Resource)
	for _, p := range o.Matched {
		fmt.Fprintf(&b, "matched: %s/%s (priority=%d)\n", p.Kind, p.Name, p.Priority)
	}
	for _, p := range o.Rejected {
		fmt.Fprintf(&b, "rejected: %s/%s — %s\n", p.Kind, p.Name, p.Reason)
	}
	for _, f := range o.RowFilters {
		fmt.Fprintf(&b, "row filter on: %s\n", f)
	}
	for _, m := range o.ColumnMasks {
		fmt.Fprintf(&b, "mask: %s → %s (policy %s)\n", m.Column, m.MaskType, m.Policy)
	}
	return b.String()
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
	user, _ := userFrom(ctx)
	tables, err := s.deps.Service.ListTables(ctx, in.Catalog, in.Schema, user)
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
	user, _ := userFrom(ctx)
	ref, err := parseTableRef(in.Table)
	if err != nil {
		return toolErrorResult(pkgerr.Newf(pkgerr.CodeSyntax, "%s", err.Error())),
			DescribeTableOutput{}, nil
	}
	entry, err := s.deps.Service.DescribeTable(ctx, ref, user)
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
