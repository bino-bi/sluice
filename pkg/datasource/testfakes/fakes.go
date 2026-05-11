// SPDX-License-Identifier: Apache-2.0

package testfakes

import (
	"context"
	"database/sql"

	"github.com/bino-bi/sluice/pkg/datasource"
)

// fakeDataSource is an in-memory DataSource for unit tests.
type fakeDataSource struct {
	name     string
	typ      string
	readonly bool
	schema   datasource.Schema
	closed   bool

	attachHook func(ctx context.Context, conn *sql.Conn, opts datasource.AttachOptions) error
	healthHook func(ctx context.Context) error
}

// Option configures a New fake.
type Option func(*fakeDataSource)

// WithReadOnly sets the Readonly flag. Default: true.
func WithReadOnly(b bool) Option {
	return func(f *fakeDataSource) { f.readonly = b }
}

// WithType overrides the Type() return. Default: "fake".
func WithType(typ string) Option {
	return func(f *fakeDataSource) { f.typ = typ }
}

// WithAttachHook installs a custom Attach implementation. Default: no-op.
func WithAttachHook(fn func(ctx context.Context, conn *sql.Conn, opts datasource.AttachOptions) error) Option {
	return func(f *fakeDataSource) { f.attachHook = fn }
}

// WithHealthHook installs a custom HealthCheck implementation. Default: no-op.
func WithHealthHook(fn func(ctx context.Context) error) Option {
	return func(f *fakeDataSource) { f.healthHook = fn }
}

// New returns an in-memory DataSource suitable for unit tests.
func New(catalog string, schema datasource.Schema, opts ...Option) datasource.DataSource {
	f := &fakeDataSource{
		name:     catalog,
		typ:      "fake",
		readonly: true,
		schema:   schema,
	}
	for _, o := range opts {
		o(f)
	}
	return f
}

// Name implements datasource.DataSource.
func (f *fakeDataSource) Name() string { return f.name }

// Type implements datasource.DataSource.
func (f *fakeDataSource) Type() string { return f.typ }

// Readonly implements datasource.DataSource.
func (f *fakeDataSource) Readonly() bool { return f.readonly }

// Attach implements datasource.DataSource.
func (f *fakeDataSource) Attach(ctx context.Context, conn *sql.Conn, opts datasource.AttachOptions) error {
	if f.closed {
		return datasource.ErrClosed
	}
	if f.attachHook != nil {
		return f.attachHook(ctx, conn, opts)
	}
	return nil
}

// Schema implements datasource.DataSource.
func (f *fakeDataSource) Schema(_ context.Context, _ *sql.Conn) (datasource.Schema, error) {
	if f.closed {
		return datasource.Schema{}, datasource.ErrClosed
	}
	return f.schema, nil
}

// HealthCheck implements datasource.DataSource.
func (f *fakeDataSource) HealthCheck(ctx context.Context, _ *sql.Conn, _ datasource.HealthOptions) error {
	if f.closed {
		return datasource.ErrClosed
	}
	if f.healthHook != nil {
		return f.healthHook(ctx)
	}
	return nil
}

// Close implements datasource.DataSource.
func (f *fakeDataSource) Close() error {
	f.closed = true
	return nil
}
