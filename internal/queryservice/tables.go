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

// ListCatalogs returns the attached catalogs the user is able to see.
// MVP does not filter by policy — admin/operator surfaces are informative
// only. v1 adds per-user visibility.
func (s *Service) ListCatalogs(ctx context.Context, lister CatalogLister, user *identity.UserCtx) ([]CatalogInfo, error) {
	if lister == nil {
		return nil, pkgerr.New(pkgerr.CodeInternal).WithMessage("catalog lister not configured")
	}
	_ = user // reserved for v1 per-user filtering
	return lister.List(ctx), nil
}

// ListTables reports every table in (catalog, schemaName). Backed by
// schema.Cache; requires an Entry for the target.
func (s *Service) ListTables(ctx context.Context, catalog, schemaName string) ([]parser.TableRef, error) {
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
		out = append(out, parser.TableRef{
			Catalog: e.Key.Catalog,
			Schema:  e.Key.Schema,
			Table:   e.Key.Table,
		})
	}
	return out, nil
}

// DescribeTable returns the cached schema.Entry for key, refreshing on
// miss. Errors propagate untouched (schema.Cache already returns
// domain-appropriate sentinels).
func (s *Service) DescribeTable(ctx context.Context, ref parser.TableRef) (*schema.Entry, error) {
	if s.opts.Schema == nil {
		return nil, pkgerr.New(pkgerr.CodeInternal).WithMessage("schema cache not configured")
	}
	key := schema.Key{Catalog: ref.Catalog, Schema: ref.Schema, Table: ref.Table}
	return s.opts.Schema.Get(ctx, key)
}
