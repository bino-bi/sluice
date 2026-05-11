// SPDX-License-Identifier: AGPL-3.0-or-later

package rest

import (
	"net/http"

	"github.com/bino-bi/sluice/internal/version"
)

type versionResponse struct {
	Version       string `json:"version"`
	Commit        string `json:"commit,omitempty"`
	BuildTime     string `json:"build_time,omitempty"`
	Go            string `json:"go,omitempty"`
	DuckDB        string `json:"duckdb,omitempty"`
	ParserVersion string `json:"parser_version,omitempty"`
}

// handleVersion echoes the compiled build identity. Used by operators,
// clients, and /v1/health callers wanting richer output.
func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	b := version.Current()
	writeJSON(w, http.StatusOK, versionResponse{
		Version:       b.Version,
		Commit:        b.Commit,
		BuildTime:     b.BuildTime.UTC().Format("2006-01-02T15:04:05Z"),
		Go:            b.GoVersion,
		ParserVersion: version.PgQueryVersion(),
	})
}

// handleOpenAPI returns a stub OpenAPI document. The generator landing in
// Slice 10 will replace this with a proper spec; for MVP we at least
// advertise the endpoint so clients can feature-detect it.
func (s *Server) handleOpenAPI(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":   "Sluice",
			"version": version.Current().Version,
		},
		"paths": map[string]any{
			"/v1/query":   map[string]any{},
			"/v1/health":  map[string]any{},
			"/v1/ready":   map[string]any{},
			"/v1/version": map[string]any{},
		},
	})
}
