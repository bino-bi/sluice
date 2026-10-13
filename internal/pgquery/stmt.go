// SPDX-License-Identifier: AGPL-3.0-or-later

package pgquery

import (
	pg "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/bino-bi/sluice/internal/parser"
)

// statementKind inspects the top-level RawStmt and maps it to parser.StmtKind.
// Unrecognised statements map to StmtUnsupported; the rewriter rejects them
// regardless of policy.
func statementKind(raw *pg.RawStmt) parser.StmtKind {
	if raw == nil || raw.Stmt == nil {
		return parser.StmtUnsupported
	}
	switch v := raw.Stmt.Node.(type) {
	case *pg.Node_SelectStmt:
		return selectKind(v.SelectStmt)
	case *pg.Node_ExplainStmt:
		// EXPLAIN ANALYZE runs its body, and the rewrite passes only secure
		// a SELECT body. Any other body is classified as what it is.
		body := v.ExplainStmt.GetQuery()
		if sel := body.GetSelectStmt(); sel != nil && selectKind(sel) == parser.StmtSelect {
			return parser.StmtExplain
		}
		return statementKind(&pg.RawStmt{Stmt: body})
	case *pg.Node_VariableSetStmt:
		return parser.StmtSet
	case *pg.Node_VariableShowStmt:
		return parser.StmtShow
	case *pg.Node_InsertStmt:
		return parser.StmtInsert
	case *pg.Node_UpdateStmt:
		return parser.StmtUpdate
	case *pg.Node_DeleteStmt:
		return parser.StmtDelete
	case *pg.Node_MergeStmt:
		return parser.StmtUpdate // MERGE writes; treat as UPDATE for ACL classification.
	case *pg.Node_CopyStmt:
		return parser.StmtCopy
	case *pg.Node_TransactionStmt:
		return parser.StmtUnsupported
	case *pg.Node_CreateStmt,
		*pg.Node_AlterTableStmt,
		*pg.Node_DropStmt,
		*pg.Node_CreateTableAsStmt,
		*pg.Node_CreateSchemaStmt,
		*pg.Node_IndexStmt,
		*pg.Node_ViewStmt,
		*pg.Node_CreateFunctionStmt,
		*pg.Node_CreatePolicyStmt,
		*pg.Node_AlterPolicyStmt,
		*pg.Node_CreateExtensionStmt,
		*pg.Node_AlterExtensionStmt,
		*pg.Node_GrantStmt,
		*pg.Node_GrantRoleStmt:
		return parser.StmtDDL
	}
	return parser.StmtUnsupported
}

// selectKind classifies a SELECT. SELECT ... INTO creates a table, and a
// CTE can hold an INSERT, UPDATE, DELETE or MERGE, at any depth. DuckDB
// refuses both today; the statement gate must not rely on that.
func selectKind(sel *pg.SelectStmt) parser.StmtKind {
	kind := parser.StmtSelect
	if sel == nil {
		return kind
	}
	walkMessages(sel.ProtoReflect(), func(m protoreflect.Message) bool {
		switch v := m.Interface().(type) {
		case *pg.SelectStmt:
			if v.IntoClause != nil {
				kind = parser.StmtDDL
			}
		case *pg.CommonTableExpr:
			if q := v.Ctequery; q != nil && q.GetSelectStmt() == nil {
				kind = statementKind(&pg.RawStmt{Stmt: q})
			}
		}
		return kind == parser.StmtSelect
	})
	return kind
}
