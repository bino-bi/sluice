// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice

import (
	"context"

	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/schema"
	pkgapi "github.com/bino-bi/sluice/pkg/apitypes"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// ExplainInput captures a "would this query be allowed?" request. Either
// Table or SimulatedSQL must be set.
type ExplainInput struct {
	User         *identity.UserCtx
	Table        parser.TableRef
	SimulatedSQL string
	Origin       Origin
}

// Explain delegates to the policy engine, wrapping the call in an
// admin-action audit event. The engine renders the same decision the
// live path would produce.
func (s *Service) Explain(ctx context.Context, in ExplainInput) (*pkgapi.ExplainResult, error) {
	start := s.opts.Clock()
	qid := pkgerr.NewQueryID()

	var (
		ast    parser.AST
		shape  parser.QueryShape
		tables []parser.TableRef
	)
	if in.SimulatedSQL != "" {
		a, err := s.opts.Parser.Parse(ctx, in.SimulatedSQL)
		if err != nil {
			return nil, parseErrToAPI(err, qid)
		}
		ast = a
		shape = a.Shape()
		tables = a.Tables()
	}
	if in.Table.Table != "" {
		tables = append(tables, in.Table)
	}

	result, err := s.opts.Policy.Explain(ctx, policy.Input{
		User: in.User, AST: ast, Shape: shape, Tables: tables, Now: start,
	})
	rec := &audit.Record{
		EventType: audit.EventAdminAction,
		QueryID:   qid,
		Origin:    string(in.Origin),
		Subject:   subjectFromUser(in.User),
		Tables:    tablesToStrings(tables),
		Catalogs:  catalogsFromTables(tables),
		Message:   "policy explain",
	}
	if err != nil {
		setErrorCode(rec, err)
		rec.Decision = audit.DecisionError
	} else if result != nil {
		rec.Decision = result.Effective.Decision
	}
	s.emit(ctx, rec, start)
	if err != nil {
		return nil, toAPIError(err, qid)
	}
	return result, nil
}

// CatalogInfo summarises one attached data source. The queryservice
// doesn't own datasource state; the transport layer feeds a CatalogLister
// implementation that wraps *datasource.Registry.
type CatalogInfo struct {
	Name        string
	Type        string
	Healthy     bool
	SchemaCount int
}

// CatalogLister is the narrow interface the transport provides to expose
// catalogs without forcing internal/queryservice to depend on
// internal/datasource.
type CatalogLister interface {
	List(ctx context.Context) []CatalogInfo
}

// ListCatalogs returns the attached catalogs the user is able to see. A
// catalog is hidden when every table the schema cache knows about in it is
// denied to the user; catalogs with no known tables stay visible (the
// operator attached them and we cannot prove they are off-limits).
func (s *Service) ListCatalogs(ctx context.Context, lister CatalogLister, user *identity.UserCtx) ([]CatalogInfo, error) {
	if lister == nil {
		return nil, pkgerr.New(pkgerr.CodeInternal).WithMessage("catalog lister not configured")
	}
	all := lister.List(ctx)
	if s.opts.Schema == nil {
		return all, nil
	}
	known := map[string]bool{}
	visible := map[string]bool{}
	for _, e := range s.opts.Schema.All() {
		if e == nil {
			continue
		}
		known[e.Key.Catalog] = true
		if s.tableVisible(ctx, user, parser.TableRef{Catalog: e.Key.Catalog, Schema: e.Key.Schema, Table: e.Key.Table}) {
			visible[e.Key.Catalog] = true
		}
	}
	out := make([]CatalogInfo, 0, len(all))
	for _, c := range all {
		if known[c.Name] && !visible[c.Name] {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// ListTables reports the tables in (catalog, schemaName) that user is
// allowed to read. Backed by schema.Cache; each candidate is checked
// against the policy engine so schema discovery respects access control.
func (s *Service) ListTables(ctx context.Context, catalog, schemaName string, user *identity.UserCtx) ([]parser.TableRef, error) {
	if s.opts.Schema == nil {
		return nil, pkgerr.New(pkgerr.CodeInternal).WithMessage("schema cache not configured")
	}
	all := s.opts.Schema.All()
	out := make([]parser.TableRef, 0, len(all))
	for _, e := range all {
		if e == nil {
			continue
		}
		if e.Key.Catalog != catalog {
			continue
		}
		if schemaName != "" && e.Key.Schema != schemaName {
			continue
		}
		ref := parser.TableRef{
			Catalog: e.Key.Catalog,
			Schema:  e.Key.Schema,
			Table:   e.Key.Table,
		}
		if !s.tableVisible(ctx, user, ref) {
			continue
		}
		out = append(out, ref)
	}
	return out, nil
}

// DescribeTable returns the cached schema.Entry for ref, refreshing on
// miss. Access is checked first so an agent cannot enumerate the columns
// of a table policy denies it. Errors propagate untouched (schema.Cache
// already returns domain-appropriate sentinels).
func (s *Service) DescribeTable(ctx context.Context, ref parser.TableRef, user *identity.UserCtx) (*schema.Entry, error) {
	if s.opts.Schema == nil {
		return nil, pkgerr.New(pkgerr.CodeInternal).WithMessage("schema cache not configured")
	}
	if !s.tableVisible(ctx, user, ref) {
		return nil, pkgerr.New(pkgerr.CodeACLDenied).WithMessage("access to table denied")
	}
	key := schema.Key{Catalog: ref.Catalog, Schema: ref.Schema, Table: ref.Table}
	return s.opts.Schema.Get(ctx, key)
}

// AccessibleTables returns every table (optionally restricted to catalog)
// that user is allowed to read, across all known catalogs. Backs the MCP
// list_accessible_tables tool so an agent can discover what it may query
// without probing by trial and error.
func (s *Service) AccessibleTables(ctx context.Context, user *identity.UserCtx, catalog string) ([]parser.TableRef, error) {
	if s.opts.Schema == nil {
		return nil, pkgerr.New(pkgerr.CodeInternal).WithMessage("schema cache not configured")
	}
	all := s.opts.Schema.All()
	out := make([]parser.TableRef, 0, len(all))
	for _, e := range all {
		if e == nil {
			continue
		}
		if catalog != "" && e.Key.Catalog != catalog {
			continue
		}
		ref := parser.TableRef{Catalog: e.Key.Catalog, Schema: e.Key.Schema, Table: e.Key.Table}
		if s.tableVisible(ctx, user, ref) {
			out = append(out, ref)
		}
	}
	return out, nil
}

// tableVisible reports whether user may read ref — i.e. the policy engine
// does not deny an access to it. Used by every metadata surface so schema
// discovery cannot leak tables the caller could not query.
func (s *Service) tableVisible(ctx context.Context, user *identity.UserCtx, ref parser.TableRef) bool {
	if s.opts.Policy == nil {
		return false
	}
	dec, err := s.opts.Policy.Evaluate(ctx, policy.Input{User: user, Tables: []parser.TableRef{ref}})
	if err != nil || dec == nil {
		return false
	}
	return dec.Outcome != policy.OutcomeDeny
}
