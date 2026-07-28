// SPDX-License-Identifier: AGPL-3.0-or-later

package httpfile

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/bino-bi/sluice/internal/datasource/drivers/common"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgds "github.com/bino-bi/sluice/pkg/datasource"
)

// Type matches apitypes.DSHTTP.
const Type = "http"

const (
	secretTypeHTTP   = "http"
	secretNamePrefix = "sluice_http_"
)

// viewSpec is one pre-resolved view; every failure mode (bad URL, bad
// format, name collision) surfaces at construction.
type viewSpec struct {
	url    string
	name   string
	format apitypes.FileFormat
}

// driver exposes an explicit list of HTTP(S) URLs as read-only views.
type driver struct {
	name       string
	urls       []string
	views      []viewSpec
	origins    []string // sorted distinct origins, for header-secret scoping
	needsExcel bool

	headersRef string

	mu        sync.Mutex
	extLoaded map[*sql.Conn]bool
}

// Register installs the factory.
func Register() { pkgds.Register(Type, newDriver) }

func newDriver(_ context.Context, spec pkgds.Spec) (pkgds.DataSource, error) {
	var urls []string
	if raw, ok := spec.Raw["urls"]; ok {
		switch v := raw.(type) {
		case []string:
			urls = append(urls, v...)
		case []any:
			for _, e := range v {
				if s, ok := e.(string); ok {
					urls = append(urls, s)
				}
			}
		}
	}
	if len(urls) == 0 {
		return nil, errors.New("http: spec.urls is required")
	}
	headersRef, _ := spec.Raw["headersRef"].(string)
	formatOverride, _ := spec.Raw["format"].(string)

	views, origins, needsExcel, err := resolveViews(urls, apitypes.FileFormat(formatOverride))
	if err != nil {
		return nil, err
	}

	return &driver{
		name:       spec.Name,
		urls:       urls,
		views:      views,
		origins:    origins,
		needsExcel: needsExcel,
		headersRef: headersRef,
		extLoaded:  make(map[*sql.Conn]bool),
	}, nil
}

// resolveViews validates every URL and maps it to a named,
// format-resolved view. The URL list is the allowlist: globs are
// rejected, and each URL must name a concrete file path.
func resolveViews(urls []string, override apitypes.FileFormat) ([]viewSpec, []string, bool, error) {
	views := make([]viewSpec, 0, len(urls))
	seen := map[string]string{} // view name → url
	originSet := map[string]bool{}
	needsExcel := false
	for i, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, nil, false, fmt.Errorf("http: urls[%d] %q: not an absolute http(s) URL", i, raw)
		}
		if strings.ContainsAny(u.Path, "*[") {
			return nil, nil, false, fmt.Errorf("http: urls[%d] %q: glob patterns are not supported; list each file", i, raw)
		}
		if strings.Trim(u.Path, "/") == "" {
			return nil, nil, false, fmt.Errorf("http: urls[%d] %q: URL has no file path", i, raw)
		}

		format := override
		if format == "" {
			f, err := common.InferFormat(raw)
			if err != nil {
				return nil, nil, false, fmt.Errorf("http: urls[%d]: %w", i, err)
			}
			format = f
		}
		if format == apitypes.FileFormatXLSX {
			needsExcel = true
		}

		name := common.SanitizeViewName(strings.Trim(u.Path, "/"))
		if err := common.ValidateIdentifier(name); err != nil {
			return nil, nil, false, fmt.Errorf("http: view %q: %w", name, err)
		}
		if prev, dup := seen[name]; dup {
			return nil, nil, false, fmt.Errorf("http: urls[%d] %q: view name %q collides with %q", i, raw, name, prev)
		}
		seen[name] = raw
		views = append(views, viewSpec{url: raw, name: name, format: format})
		originSet[u.Scheme+"://"+u.Host] = true
	}
	origins := make([]string, 0, len(originSet))
	for o := range originSet {
		origins = append(origins, o)
	}
	sort.Strings(origins)
	return views, origins, needsExcel, nil
}

// Name implements pkgds.DataSource.
func (d *driver) Name() string { return d.name }

// Type implements pkgds.DataSource.
func (d *driver) Type() string { return Type }

// Readonly implements pkgds.DataSource. HTTP sources are always
// read-only.
func (d *driver) Readonly() bool { return true }

// ExternalPaths implements pkgds.ExternalPather: the exact URL list is
// the carve-out — nothing else on those hosts becomes readable.
func (d *driver) ExternalPaths() []string {
	return append([]string(nil), d.urls...)
}

// Attach loads httpfs (and excel when needed), installs the per-origin
// header secrets when configured, attaches an in-memory catalog, and
// exposes one VIEW per URL.
func (d *driver) Attach(ctx context.Context, conn *sql.Conn, opts pkgds.AttachOptions) error {
	catalog := opts.CatalogName
	if catalog == "" {
		catalog = d.name
	}
	if err := common.ValidateIdentifier(catalog); err != nil {
		return fmt.Errorf("http: catalog alias: %w", err)
	}

	extensions := []string{"httpfs"}
	if d.needsExcel {
		extensions = append(extensions, "excel")
	}
	d.mu.Lock()
	if !d.extLoaded[conn] {
		for _, ext := range extensions {
			if err := common.EnsureExtension(ctx, conn, ext); err != nil {
				d.mu.Unlock()
				return fmt.Errorf("http: %w", err)
			}
		}
		d.extLoaded[conn] = true
	}
	d.mu.Unlock()

	if err := d.createHeaderSecrets(ctx, conn, opts); err != nil {
		return err
	}

	attachStmt := fmt.Sprintf(`ATTACH ':memory:' AS %s`, catalog)
	if _, err := conn.ExecContext(ctx, attachStmt); err != nil {
		if !common.IsAlreadyAttached(err) {
			return fmt.Errorf("http: ATTACH %s: %w", catalog, err)
		}
	}

	for _, v := range d.views {
		reader, err := common.ReaderSQL(v.format, v.url)
		if err != nil {
			return fmt.Errorf("http: view %q: %w", v.name, err)
		}
		stmt := fmt.Sprintf(`CREATE OR REPLACE VIEW %s.main.%s AS SELECT * FROM %s`,
			catalog, v.name, reader)
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("http: CREATE VIEW %s: %w", v.name, err)
		}
	}
	return nil
}

// createHeaderSecrets resolves headersRef (if set) into a JSON object of
// header name → value and issues one CREATE OR REPLACE SECRET (TYPE
// HTTP, EXTRA_HTTP_HEADERS …) per URL origin, scoped to that origin so
// the headers are never sent elsewhere.
func (d *driver) createHeaderSecrets(ctx context.Context, conn *sql.Conn, opts pkgds.AttachOptions) error {
	if d.headersRef == "" {
		return nil
	}
	if opts.SecretResolver == nil {
		return errors.New("http: headersRef set but SecretResolver is nil")
	}
	raw, err := opts.SecretResolver.Resolve(ctx, d.headersRef)
	if err != nil {
		return fmt.Errorf("http: resolve headers: %w", err)
	}
	var headers map[string]string
	if err := json.Unmarshal(raw, &headers); err != nil {
		return fmt.Errorf("http: headers must be a JSON object of header name → value: %w", err)
	}
	if len(headers) == 0 {
		return nil
	}

	for i, origin := range d.origins {
		secretName := fmt.Sprintf("%s%s_%d", secretNamePrefix, d.name, i)
		if err := common.ValidateIdentifier(secretName); err != nil {
			return fmt.Errorf("http: secret name: %w", err)
		}
		stmt, err := common.BuildCreateSecret(secretName, secretTypeHTTP, []common.SecretArg{
			{Key: "EXTRA_HTTP_HEADERS", Value: common.MapLiteral(headers), Raw: true},
			{Key: "SCOPE", Value: origin},
		})
		if err != nil {
			return fmt.Errorf("http: render CREATE SECRET: %w", err)
		}
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("http: CREATE SECRET for %s: %w", origin, err)
		}
	}
	return nil
}

// Schema reports the views this driver exposed under the attached
// catalog.
func (d *driver) Schema(ctx context.Context, conn *sql.Conn) (pkgds.Schema, error) {
	sch, err := common.IntrospectCatalog(ctx, conn, d.name)
	if err != nil {
		return pkgds.Schema{}, fmt.Errorf("http: %w", err)
	}
	return sch, nil
}

// HealthCheck runs a trivial query that exercises the attach without
// touching the network — a dead endpoint shows up as a query-time
// failure, mirroring the s3 driver's posture.
func (d *driver) HealthCheck(ctx context.Context, conn *sql.Conn, opts pkgds.HealthOptions) error {
	q := opts.Query
	if q == "" {
		q = fmt.Sprintf(`SELECT 1 FROM information_schema.schemata WHERE catalog_name = '%s' LIMIT 1`,
			common.EscapeSQLString(d.name))
	}
	row := conn.QueryRowContext(ctx, q)
	var one int
	if err := row.Scan(&one); err != nil {
		return fmt.Errorf("http: health: %w", err)
	}
	return nil
}

// Close drops tracked per-connection state.
func (d *driver) Close() error {
	d.mu.Lock()
	d.extLoaded = nil
	d.mu.Unlock()
	return nil
}
