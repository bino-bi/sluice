// SPDX-License-Identifier: AGPL-3.0-or-later

package pgquery

import (
	pg "github.com/pganalyze/pg_query_go/v6"

	"github.com/bino-bi/sluice/internal/parser"
)

// pg_query parses and deparses in C with a recursive descent; past a depth
// the C stack overflows and the process dies, unrecoverable from Go. The
// crash depth depends on the node kind (measured on the default 8 MiB Linux
// thread stack): nested scalar subqueries and nested CASE segfault earliest
// (around 1400 and 1900), a left-deep operator or JOIN chain around 4500,
// set operations around 8000. The limits below sit well under those and far
// above any real query. Flat constructs (long IN lists, AND/OR chains, wide
// select lists, VALUES with many rows) do not nest and are not counted.
const (
	// maxSubqueryDepth bounds subquery and CASE nesting, which crashes
	// earliest.
	maxSubqueryDepth = 500
	// maxExprDepth bounds the true depth of the expression tree (bracket
	// nesting and left-deep operator chains together). It is the sum of the
	// operator runs along the whole open-bracket path, not of one level, so
	// a shape like ((((...)+1+1)+1+1)...) whose true depth is the product of
	// the paren nesting and the per-level operator count is counted in full.
	maxExprDepth = 2000
	// maxJoinDepth and maxSetDepth bound left-deep JOIN and set-operation
	// chains. Set operations crash much deeper, so their limit is higher.
	maxJoinDepth = 2000
	maxSetDepth  = 4000
)

// exprFrame is one open-bracket level of the expression tree.
type exprFrame struct {
	run   int // operators seen at this level since the last separator
	below int // depth already committed below this level's spine, from
	// brackets that closed at this level
	heavy bool // a subquery or CASE level (crashes earliest)
}

// checkDepth rejects a statement whose nesting would overflow pg_query's C
// stack. It reads the token stream (lexing does not recurse, so it is safe
// on the same input that would crash the parser) and tracks the true
// expression-tree depth plus independent JOIN and set-operation chain
// lengths. toks is empty after a scan error, which is left for Parse to
// report as the real syntax error.
func checkDepth(toks []*pg.ScanToken) error {
	var stack []exprFrame
	cur := exprFrame{}
	heavy := 0
	joins, sets := 0, 0

	depth := func() int { return len(stack) + cur.below + cur.run }

	for i, tok := range toks {
		switch {
		case isOpenBracket(tok.Token):
			h := tok.Token == pg.Token_CASE || opensSubquery(toks, i)
			if h {
				heavy++
				if heavy > maxSubqueryDepth {
					return parser.ErrTooDeep
				}
			}
			stack = append(stack, cur)
			cur = exprFrame{heavy: h}
			if depth() > maxExprDepth {
				return parser.ErrTooDeep
			}
		case isCloseBracket(tok.Token):
			if n := len(stack); n > 0 {
				closed := cur
				if closed.heavy {
					heavy--
				}
				cur = stack[n-1]
				stack = stack[:n-1]
				// The closed bracket becomes the left operand of whatever
				// operators follow at this level, so its depth stacks onto
				// this level's spine.
				cur.below += closed.below + closed.run + 1
				if depth() > maxExprDepth {
					return parser.ErrTooDeep
				}
			}
		case tok.Token == pg.Token_JOIN:
			joins++
			if joins > maxJoinDepth {
				return parser.ErrTooDeep
			}
			cur.run = 0
		case isSetOp(tok.Token):
			sets++
			if sets > maxSetDepth {
				return parser.ErrTooDeep
			}
			cur.run = 0
		case isDepthSeparator(tok.Token):
			// A new sub-expression starts here; the spine does not carry
			// across it.
			cur.run = 0
			cur.below = 0
		case isOperand(tok.Token):
			// Leaves (identifiers, literals, parameters) do not add depth.
		default:
			// Everything else glues operands into a nested node: infix and
			// prefix operators, and the postfix predicate keywords (IS NULL,
			// IS TRUE, IS JSON, ISNULL, NOTNULL, ...) that chain to arbitrary
			// depth without a bracket. Counting the complement rather than a
			// fixed operator list is fail-closed: a keyword this scan does
			// not know is treated as depth, never silently skipped.
			cur.run++
			if depth() > maxExprDepth {
				return parser.ErrTooDeep
			}
		}
	}
	return nil
}

// opensSubquery reports whether the bracket token at index i opens a
// subquery rather than a grouping or function-argument list. A subquery
// level nests a whole query and costs far more C stack than an arithmetic
// group.
func opensSubquery(toks []*pg.ScanToken, i int) bool {
	for j := i + 1; j < len(toks); j++ {
		switch toks[j].Token {
		case pg.Token_SQL_COMMENT, pg.Token_C_COMMENT:
			continue
		case pg.Token_SELECT, pg.Token_WITH, pg.Token_VALUES, pg.Token_TABLE:
			return true
		default:
			return false
		}
	}
	return false
}

func isOpenBracket(t pg.Token) bool {
	return t == pg.Token_ASCII_40 || t == pg.Token_ASCII_91 || t == pg.Token_CASE
}

func isCloseBracket(t pg.Token) bool {
	return t == pg.Token_ASCII_41 || t == pg.Token_ASCII_93 || t == pg.Token_END_P
}

func isSetOp(t pg.Token) bool {
	return t == pg.Token_UNION || t == pg.Token_INTERSECT || t == pg.Token_EXCEPT
}

// isOperand reports whether t is a leaf of the expression tree: a literal,
// a parameter, an identifier, a truth-value, or the qualifier dot. A leaf
// adds no depth. Comment tokens are treated the same (they are not part of
// the tree). Everything that is neither a leaf, a bracket, a separator,
// JOIN nor a set operation is counted as depth-adding (see checkDepth).
func isOperand(t pg.Token) bool {
	switch t {
	case pg.Token_IDENT,
		pg.Token_UIDENT,
		pg.Token_FCONST,
		pg.Token_SCONST,
		pg.Token_USCONST,
		pg.Token_BCONST,
		pg.Token_XCONST,
		pg.Token_ICONST,
		pg.Token_PARAM,
		pg.Token_TRUE_P,
		pg.Token_FALSE_P,
		pg.Token_NULL_P,
		pg.Token_ASCII_46, // . (qualifier separator inside a name)
		pg.Token_SQL_COMMENT,
		pg.Token_C_COMMENT:
		return true
	}
	return false
}

// isDepthSeparator reports whether t ends the current operator chain. These
// keywords delimit the arms of a flat construct, so an operator after one
// starts a new chain rather than deepening the old.
func isDepthSeparator(t pg.Token) bool {
	switch t {
	case pg.Token_ASCII_44, // ,
		pg.Token_AND,
		pg.Token_OR,
		pg.Token_WHEN,
		pg.Token_THEN,
		pg.Token_ELSE,
		pg.Token_FROM,
		pg.Token_WHERE,
		pg.Token_HAVING,
		pg.Token_ORDER,
		pg.Token_BY,
		pg.Token_LIMIT,
		pg.Token_OFFSET,
		pg.Token_ON,
		pg.Token_USING,
		pg.Token_SELECT,
		pg.Token_VALUES,
		pg.Token_BETWEEN,
		pg.Token_DISTINCT,
		pg.Token_OVER,
		pg.Token_PARTITION,
		pg.Token_WINDOW,
		pg.Token_WITH,
		pg.Token_AS,
		pg.Token_SET,
		pg.Token_INTO,
		pg.Token_RETURNING:
		return true
	}
	return false
}
