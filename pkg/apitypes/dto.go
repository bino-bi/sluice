// SPDX-License-Identifier: Apache-2.0

package apitypes

// QueryRequest is the REST + MCP wire shape for a query.
type QueryRequest struct {
	SQL       string            `json:"sql"`
	Params    []any             `json:"params,omitempty"`
	MaxRows   int               `json:"max_rows,omitempty"`
	TimeoutMS int               `json:"timeout_ms,omitempty"`
	Format    ResponseFormat    `json:"format,omitempty"`
	Meta      map[string]string `json:"meta,omitempty"`
}

// ResponseFormat names the response encoding. FormatJSON is the default
// used when a client does not set Format explicitly.
type ResponseFormat string

const (
	FormatJSON  ResponseFormat = "json"
	FormatCSV   ResponseFormat = "csv"
	FormatArrow ResponseFormat = "arrow"
)

// QueryResponse is the successful wire shape for a query.
type QueryResponse struct {
	QueryID   string   `json:"query_id"`
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	RowCount  int64    `json:"row_count"`
	Truncated bool     `json:"truncated"`
}

// HealthStatus is the body of /v1/health.
type HealthStatus struct {
	Status      string            `json:"status"`
	Version     string            `json:"version"`
	Commit      string            `json:"commit"`
	DuckDB      string            `json:"duckdb"`
	DataSources map[string]string `json:"datasources"`
	Uptime      Duration          `json:"uptime"`
}

// ExplainResult is returned by the admin "explain" endpoint and by the CLI
// `policy explain` subcommand. It reports which policies would have
// matched a given (subject, resource) pair.
type ExplainResult struct {
	Subject   string            `json:"subject"`
	Resource  string            `json:"resource"`
	Matched   []AppliedPolicy   `json:"matched"`
	Rejected  []RejectedPolicy  `json:"rejected"`
	Effective EffectiveDecision `json:"effective"`
	// Shadow lists policies that matched but ran in Audit / DryRun mode and
	// therefore did not affect the effective decision.
	Shadow []AppliedPolicy `json:"shadow,omitempty"`
}

// AppliedPolicy names a policy whose selector matched.
type AppliedPolicy struct {
	Kind     Kind   `json:"kind"`
	Name     string `json:"name"`
	Priority int32  `json:"priority"`
}

// RejectedPolicy names a policy whose selector matched and whose decision
// was to reject the request.
type RejectedPolicy struct {
	Kind   Kind   `json:"kind"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// EffectiveDecision summarizes the resolution of all applied policies.
type EffectiveDecision struct {
	Decision    string          `json:"decision"`
	RowFilters  []string        `json:"row_filters,omitempty"`
	ColumnMasks []ColumnMaskRef `json:"column_masks,omitempty"`
}

// ColumnMaskRef identifies a mask that was or would be applied.
type ColumnMaskRef struct {
	Column   string   `json:"column"`
	MaskType MaskType `json:"mask_type"`
	Policy   string   `json:"policy"`
}
