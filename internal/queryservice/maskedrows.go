// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice

import (
	"context"
	"fmt"

	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/rewriter"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
	pkgmask "github.com/bino-bi/sluice/pkg/mask"
)

// identityView adapts identity.UserCtx to the read-only pkg/mask.Identity
// interface that post-query mask providers receive.
type identityView struct{ u *identity.UserCtx }

func (v identityView) Subject() string {
	if v.u == nil {
		return ""
	}
	return v.u.Subject
}

func (v identityView) Groups() []string {
	if v.u == nil {
		return nil
	}
	return v.u.Groups
}

func (v identityView) Claim(name string) (any, bool) {
	if v.u == nil || v.u.Claims == nil {
		return nil, false
	}
	c, ok := v.u.Claims[name]
	return c, ok
}

// buildMaskedRows wraps inner with a decorator that applies each post-query
// mask to its bound result column. It is called after Execute and before
// the fail-closed audit gate, so any provider construction failure refuses
// the query without serving or falsely auditing a row.
func (s *Service) buildMaskedRows(ctx context.Context, inner executor.RowIterator, user identityView, posts []rewriter.PostMask) (executor.RowIterator, error) {
	if len(posts) == 0 {
		return inner, nil
	}
	reg := s.opts.Masks
	if reg == nil {
		reg = pkgmask.Default()
	}
	masks := make(map[int]pkgmask.RowMask, len(posts))
	for _, pm := range posts {
		provider, ok := reg.Lookup(string(pm.Type))
		if !ok {
			return nil, pkgerr.New(pkgerr.CodeInternal).
				WithMessage(fmt.Sprintf("mask provider %q not registered", pm.Type))
		}
		rm, ok := provider.(pkgmask.RowMasker)
		if !ok {
			return nil, pkgerr.New(pkgerr.CodeInternal).
				WithMessage(fmt.Sprintf("mask provider %q is not post-query capable", pm.Type))
		}
		catalog, schemaName, table := splitTableKey(pm.TableKey)
		mask, err := rm.NewRowMask(pkgmask.RowMaskContext{
			Ctx:      ctx,
			Column:   pkgmask.ColumnRef{Catalog: catalog, Schema: schemaName, Table: table, Column: pm.Column},
			Args:     pm.Args,
			Identity: user,
			Keys:     s.opts.Keys,
			Salts:    s.opts.Salts,
		})
		if err != nil {
			return nil, pkgerr.New(pkgerr.CodeInternal).
				WithMessage(fmt.Sprintf("build mask for %s.%s: %v", pm.TableKey, pm.Column, err))
		}
		masks[pm.ColumnIndex] = mask
	}
	return &maskedRows{inner: inner, masks: masks}, nil
}

// maskedRows is a RowIterator decorator that masks selected columns as
// each row is scanned. It requires every masked destination to be a *any
// (the pointer shape every transport scans with); a mismatch fails closed.
type maskedRows struct {
	inner executor.RowIterator
	masks map[int]pkgmask.RowMask
}

func (m *maskedRows) Next() bool { return m.inner.Next() }
func (m *maskedRows) Err() error { return m.inner.Err() }

func (m *maskedRows) Close() error { return m.inner.Close() }

func (m *maskedRows) Scan(dest ...any) error {
	if err := m.inner.Scan(dest...); err != nil {
		return err
	}
	for idx, mask := range m.masks {
		if idx < 0 || idx >= len(dest) {
			return pkgerr.New(pkgerr.CodeInternal).
				WithMessage("post-query mask column index out of range")
		}
		ptr, ok := dest[idx].(*any)
		if !ok {
			return pkgerr.New(pkgerr.CodeInternal).
				WithMessage("post-query masking requires scanning into *any destinations")
		}
		masked, err := mask.Mask(*ptr)
		if err != nil {
			return pkgerr.New(pkgerr.CodeInternal).
				WithMessage(fmt.Sprintf("apply post-query mask: %v", err))
		}
		*ptr = masked
	}
	return nil
}

// splitTableKey splits "catalog.schema.table" into its parts.
func splitTableKey(key string) (catalog, schemaName, table string) {
	var parts []string
	start := 0
	for i := 0; i < len(key); i++ {
		if key[i] == '.' {
			parts = append(parts, key[start:i])
			start = i + 1
			if len(parts) == 2 {
				break
			}
		}
	}
	parts = append(parts, key[start:])
	switch len(parts) {
	case 3:
		return parts[0], parts[1], parts[2]
	case 2:
		return "", parts[0], parts[1]
	default:
		return "", "", key
	}
}
