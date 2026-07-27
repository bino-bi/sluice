// SPDX-License-Identifier: AGPL-3.0-or-later

package s3

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/bino-bi/sluice/internal/datasource/drivers/common"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgds "github.com/bino-bi/sluice/pkg/datasource"
)

// Type matches apitypes.DSS3.
const Type = "s3"

const (
	secretTypeS3     = "s3"
	secretNamePrefix = "sluice_s3_"
)

// viewSpec is one pre-resolved view: every failure mode (bad format,
// bad identifier, name collision) surfaces at construction, where
// FailFast/degraded-mode handling already exists.
type viewSpec struct {
	path   string
	name   string
	format apitypes.FileFormat
}

// driver holds bucket metadata and the allowedPaths whitelist. A single
// data source instance covers multiple file paths within one bucket
// (cross-bucket access requires multiple DataSource entries).
type driver struct {
	name     string
	readonly bool

	bucket       string
	prefix       string
	region       string
	endpoint     string
	allowedPaths []string
	views        []viewSpec
	needsExcel   bool

	credentialsRef string

	mu        sync.Mutex
	extLoaded map[*sql.Conn]bool
}

// s3Credentials is the expected shape of the secret payload.
type s3Credentials struct {
	KeyID        string `json:"key_id"`
	Secret       string `json:"secret"`
	SessionToken string `json:"session_token,omitempty"`
}

// Register installs the factory.
func Register() { pkgds.Register(Type, newDriver) }

func newDriver(_ context.Context, spec pkgds.Spec) (pkgds.DataSource, error) {
	bucket, _ := spec.Raw["bucket"].(string)
	if bucket == "" {
		return nil, errors.New("s3: spec.bucket is required")
	}
	region, _ := spec.Raw["region"].(string)
	if region == "" {
		return nil, errors.New("s3: spec.region is required")
	}
	prefix, _ := spec.Raw["prefix"].(string)
	endpoint, _ := spec.Raw["endpoint"].(string)
	credRef, _ := spec.Raw["credentialsRef"].(string)
	formatOverride, _ := spec.Raw["format"].(string)

	var allowed []string
	if raw, ok := spec.Raw["allowedPaths"]; ok {
		switch v := raw.(type) {
		case []string:
			allowed = append(allowed, v...)
		case []any:
			for _, e := range v {
				if s, ok := e.(string); ok {
					allowed = append(allowed, s)
				}
			}
		}
	}

	for i, p := range allowed {
		norm, err := common.NormalizeS3URI(p)
		if err != nil {
			// Allow bare paths (operator shorthand) by adding the bucket.
			if !strings.Contains(p, "://") {
				continue
			}
			return nil, fmt.Errorf("s3: allowedPaths[%d] %q: %w", i, p, err)
		}
		if norm != "" {
			allowed[i] = norm
		}
	}

	views, needsExcel, err := resolveViews(allowed, apitypes.FileFormat(formatOverride))
	if err != nil {
		return nil, err
	}

	return &driver{
		name:           spec.Name,
		readonly:       true,
		bucket:         bucket,
		prefix:         prefix,
		region:         region,
		endpoint:       endpoint,
		allowedPaths:   allowed,
		views:          views,
		needsExcel:     needsExcel,
		credentialsRef: credRef,
		extLoaded:      make(map[*sql.Conn]bool),
	}, nil
}

// resolveViews maps every allowed path to a named, format-resolved view.
// An explicit override applies to all paths; otherwise each path infers
// its format from the file extension.
func resolveViews(allowed []string, override apitypes.FileFormat) ([]viewSpec, bool, error) {
	views := make([]viewSpec, 0, len(allowed))
	seen := map[string]string{} // view name → path
	needsExcel := false
	for i, p := range allowed {
		format := override
		if format == "" {
			f, err := common.InferFormat(p)
			if err != nil {
				return nil, false, fmt.Errorf("s3: allowedPaths[%d]: %w", i, err)
			}
			format = f
		}
		if format == apitypes.FileFormatXLSX {
			// read_xlsx opens a single workbook; globs are unsupported.
			if strings.ContainsAny(p, "*?[") {
				return nil, false, fmt.Errorf("s3: allowedPaths[%d] %q: glob patterns are not supported for xlsx", i, p)
			}
			needsExcel = true
		}
		name := common.SanitizeViewName(trimBucketPrefix(p))
		if err := common.ValidateIdentifier(name); err != nil {
			return nil, false, fmt.Errorf("s3: view %q: %w", name, err)
		}
		if prev, dup := seen[name]; dup {
			return nil, false, fmt.Errorf("s3: allowedPaths[%d] %q: view name %q collides with %q", i, p, name, prev)
		}
		seen[name] = p
		views = append(views, viewSpec{path: p, name: name, format: format})
	}
	return views, needsExcel, nil
}

// Name implements pkgds.DataSource.
func (d *driver) Name() string { return d.name }

// Type implements pkgds.DataSource.
func (d *driver) Type() string { return Type }

// Readonly implements pkgds.DataSource.
func (d *driver) Readonly() bool { return d.readonly }

// ExternalPaths implements pkgds.ExternalPather: query-time reads stay
// inside the bucket, so the bucket prefix is the carve-out.
func (d *driver) ExternalPaths() []string {
	return []string{"s3://" + d.bucket + "/"}
}

// Attach loads httpfs (and excel when needed), creates the S3 secret
// (if credentials are configured), attaches an in-memory catalog under
// the configured alias, and exposes one VIEW per allowed path.
func (d *driver) Attach(ctx context.Context, conn *sql.Conn, opts pkgds.AttachOptions) error {
	catalog := opts.CatalogName
	if catalog == "" {
		catalog = d.name
	}
	if err := common.ValidateIdentifier(catalog); err != nil {
		return fmt.Errorf("s3: catalog alias: %w", err)
	}

	extensions := []string{"httpfs"}
	if d.needsExcel {
		extensions = append(extensions, "excel")
	}
	d.mu.Lock()
	if !d.extLoaded[conn] {
		for _, ext := range extensions {
			if _, err := conn.ExecContext(ctx, "INSTALL "+ext); err != nil {
				d.mu.Unlock()
				return fmt.Errorf("s3: INSTALL %s: %w", ext, err)
			}
			if _, err := conn.ExecContext(ctx, "LOAD "+ext); err != nil {
				d.mu.Unlock()
				return fmt.Errorf("s3: LOAD %s: %w", ext, err)
			}
		}
		d.extLoaded[conn] = true
	}
	d.mu.Unlock()

	if err := d.createSecret(ctx, conn, opts); err != nil {
		return err
	}

	attachStmt := fmt.Sprintf(`ATTACH ':memory:' AS %s`, catalog)
	if _, err := conn.ExecContext(ctx, attachStmt); err != nil {
		if !common.IsAlreadyAttached(err) {
			return fmt.Errorf("s3: ATTACH %s: %w", catalog, err)
		}
	}

	for _, v := range d.views {
		reader, err := common.ReaderSQL(v.format, v.path)
		if err != nil {
			return fmt.Errorf("s3: view %q: %w", v.name, err)
		}
		stmt := fmt.Sprintf(`CREATE OR REPLACE VIEW %s.main.%s AS SELECT * FROM %s`,
			catalog, v.name, reader)
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("s3: CREATE VIEW %s: %w", v.name, err)
		}
	}
	return nil
}

// trimBucketPrefix produces a short name for SanitizeViewName to work on
// — otherwise every view name starts with "s3___bucket_" which is
// noisy. Operators who want full-path names can override via a spec
// field in a later slice.
func trimBucketPrefix(uri string) string {
	rest := strings.TrimPrefix(uri, "s3://")
	if _, after, ok := strings.Cut(rest, "/"); ok {
		return after
	}
	return rest
}

// createSecret resolves the credentialsRef (if any) and issues CREATE
// OR REPLACE SECRET. When no credentials are configured the driver
// relies on DuckDB's default chain (environment / instance profile).
func (d *driver) createSecret(ctx context.Context, conn *sql.Conn, opts pkgds.AttachOptions) error {
	if d.credentialsRef == "" && d.endpoint == "" {
		// No explicit config — rely on ambient credentials. This is the
		// simplest MinIO-with-IRSA / AWS-with-IAM-role path.
		return nil
	}

	args := []common.SecretArg{
		{Key: "REGION", Value: d.region},
	}
	if d.endpoint != "" {
		// DuckDB expects ENDPOINT as host:port; the scheme (documented
		// form: "http://minio:9000") only decides USE_SSL.
		host := strings.TrimPrefix(strings.TrimPrefix(d.endpoint, "https://"), "http://")
		args = append(args, common.SecretArg{Key: "ENDPOINT", Value: host})
		// DuckDB requires url_style=path for non-AWS endpoints.
		args = append(args, common.SecretArg{Key: "URL_STYLE", Value: "path"})
		args = append(args, common.SecretArg{Key: "USE_SSL", Value: httpsOrFalse(d.endpoint)})
	}
	if d.credentialsRef != "" {
		if opts.SecretResolver == nil {
			return errors.New("s3: credentialsRef set but SecretResolver is nil")
		}
		raw, err := opts.SecretResolver.Resolve(ctx, d.credentialsRef)
		if err != nil {
			return fmt.Errorf("s3: resolve credentials: %w", err)
		}
		var creds s3Credentials
		if err := json.Unmarshal(raw, &creds); err != nil {
			return fmt.Errorf("s3: credentials must be JSON {key_id,secret[,session_token]}: %w", err)
		}
		if creds.KeyID == "" || creds.Secret == "" {
			return errors.New("s3: credentials JSON missing key_id or secret")
		}
		args = append(args,
			common.SecretArg{Key: "KEY_ID", Value: creds.KeyID},
			common.SecretArg{Key: "SECRET", Value: creds.Secret},
		)
		if creds.SessionToken != "" {
			args = append(args, common.SecretArg{Key: "SESSION_TOKEN", Value: creds.SessionToken})
		}
	}

	secretName := secretNamePrefix + d.name
	if err := common.ValidateIdentifier(secretName); err != nil {
		return fmt.Errorf("s3: secret name: %w", err)
	}
	stmt, err := common.BuildCreateSecret(secretName, secretTypeS3, args)
	if err != nil {
		return fmt.Errorf("s3: render CREATE SECRET: %w", err)
	}
	if _, err := conn.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("s3: CREATE SECRET: %w", err)
	}
	return nil
}

// httpsOrFalse inspects the endpoint URL and returns "true" for HTTPS,
// "false" for HTTP. Operators running MinIO in plaintext (common in
// local dev) must not have DuckDB reject the connection.
func httpsOrFalse(endpoint string) string {
	if strings.HasPrefix(endpoint, "http://") {
		return "false"
	}
	return "true"
}

// Schema reports the views this driver exposed under the attached
// catalog.
func (d *driver) Schema(ctx context.Context, conn *sql.Conn) (pkgds.Schema, error) {
	sch, err := common.IntrospectCatalog(ctx, conn, d.name)
	if err != nil {
		return pkgds.Schema{}, fmt.Errorf("s3: %w", err)
	}
	return sch, nil
}

// HealthCheck runs a trivial query that exercises the attach. It does
// not touch S3 — a dead bucket here would show up as a query-time
// failure; a driver-level health probe that actually HEADs S3 is
// deferred until we add a credential-rotation story.
func (d *driver) HealthCheck(ctx context.Context, conn *sql.Conn, opts pkgds.HealthOptions) error {
	q := opts.Query
	if q == "" {
		q = fmt.Sprintf(`SELECT 1 FROM information_schema.schemata WHERE catalog_name = '%s' LIMIT 1`,
			common.EscapeSQLString(d.name))
	}
	row := conn.QueryRowContext(ctx, q)
	var one int
	if err := row.Scan(&one); err != nil {
		return fmt.Errorf("s3: health: %w", err)
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

// PathAllowed is an exported helper the rewriter / admin surfaces can
// use to reject queries that reference S3 URIs outside the whitelist.
// Keeping it on the driver rather than the registry lets each data
// source own its own allowlist semantics.
func (d *driver) PathAllowed(p string) bool {
	return common.MatchAllowed(d.allowedPaths, p)
}
