// SPDX-License-Identifier: AGPL-3.0-or-later

package pgquery

import (
	pg "github.com/pganalyze/pg_query_go/v6"

	"github.com/bino-bi/sluice/internal/parser"
)

// statementKind inspects the top-level RawStmt and maps it to parser.StmtKind.
// Unrecognised statements map to StmtUnsupported; the rewriter rejects them
// regardless of policy.
func statementKind(raw *pg.RawStmt) parser.StmtKind {
	if raw == nil || raw.Stmt == nil {
		return parser.StmtUnsupported
	}
	switch raw.Stmt.Node.(type) {
	case *pg.Node_SelectStmt:
		return parser.StmtSelect
	case *pg.Node_ExplainStmt:
		return parser.StmtExplain
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
