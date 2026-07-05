// SPDX-License-Identifier: Apache-2.0

package mask

import "context"

// Provider is implemented by every column-mask type. MVP providers are SQL-
// only (MaskArrow returns ErrSQLOnly); v1 FPE and external providers opt
// into the Arrow path.
type Provider interface {
	// Type returns the canonical mask-type string (e.g. "partial"). The
	// string must match the apitypes.MaskType constant.
	Type() string

	// MaskSQL returns a single DuckDB SQL scalar expression that replaces
	// the column reference. The literal identifier __col__ marks where the
	// rewriter splices the original (qualified) column reference back in,
	// and $1..$n are provider-local positional placeholders whose values
	// are returned in params (params[k-1] binds $k; a placeholder may
	// repeat). The rewriter renumbers them into the overall query
	// parameter list. Args values must never be interpolated into the
	// snippet text — everything user-influencable binds as a param.
	MaskSQL(ctx MaskContext) (sql string, params []Param, err error)

	// MaskArrow rewrites an Arrow column after query execution. Providers
	// that only produce SQL return ErrSQLOnly. The MaskArrowContext is a
	// forward-compatible placeholder; the actual Arrow surface lands with
	// the v1 integration.
	MaskArrow(ctx MaskArrowContext) error

	// ValidateArgs runs once at policy-load time. Errors cite args field
	// names with ErrInvalidArgs wrapping.
	ValidateArgs(args Args) error
}

// MaskContext carries request-scoped inputs for MaskSQL.
type MaskContext struct {
	Ctx       context.Context
	Column    ColumnRef
	Args      Args
	Identity  Identity
	SaltStore SaltStore
}

// MaskArrowContext is a placeholder for the v1 Arrow-path MaskArrow method.
// It carries an opaque payload the Arrow integration will type when it
// lands; keeping the concrete Arrow types out of the MVP slice lets this
// package stay free of the Arrow dependency.
type MaskArrowContext struct {
	Ctx      context.Context
	Args     Args
	Identity Identity
	KeyStore KeyStore
}

// ColumnRef identifies the column being masked, populated by the rewriter
// from the parsed AST + schema cache.
type ColumnRef struct {
	Catalog string
	Schema  string
	Table   string
	Column  string
	Alias   string
	SQLType string
}

// Param is a positional parameter emitted alongside a masked SQL expression.
type Param struct {
	Name  string
	Value any
}

// Identity is a minimal, read-only subject view. Providers receive this
// instead of the full request context to limit their attack surface.
type Identity interface {
	Subject() string
	Groups() []string
	Claim(name string) (any, bool)
}

// SaltStore resolves a secret URI (e.g. "secret://env/SALT") to raw bytes.
// Implementations cache per-process and never return nil with a nil error.
type SaltStore interface {
	Get(ctx context.Context, ref string) ([]byte, error)
}

// KeyStore resolves an encryption key reference.
type KeyStore interface {
	Get(ctx context.Context, ref string) ([]byte, error)
}
