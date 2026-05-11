// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// fileProvider reads secrets from the local filesystem. URIs have the form
// secret://file//absolute/path — the double slash keeps the path absolute
// after the URL host segment consumes one slash.
//
// Permission policy:
//   - mode & 0o022 != 0 (group/world-writable) → hard error.
//   - mode & 0o004 != 0 (world-readable) → warning via slog (still returned).
type fileProvider struct {
	logger *slog.Logger
}

// Scheme implements Provider.
func (fileProvider) Scheme() string { return "file" }

// Fetch reads the file at the resolved absolute path.
func (p fileProvider) Fetch(_ context.Context, u URI) ([]byte, error) {
	path := u.Path
	if path == "" {
		return nil, fmt.Errorf("file: %q has no path", u.Raw)
	}
	// Normalize the double-slash form documented above: secret://file//abs/path
	// → URL path "//abs/path" → on-disk path "/abs/path".
	if len(path) > 1 && path[0] == '/' && path[1] == '/' {
		path = path[1:]
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("file: %q must be absolute", path)
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("file: stat %s: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("file: %s is a directory", path)
	}

	mode := info.Mode().Perm()
	if mode&0o022 != 0 {
		return nil, fmt.Errorf("file: %s is group/world-writable (mode %#o) — refusing to read secret", path, mode)
	}
	if mode&0o004 != 0 && p.logger != nil {
		p.logger.Warn("secret file is world-readable",
			slog.String("path", path),
			slog.String("mode", fmt.Sprintf("%#o", mode)),
		)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("file: read %s: %w", path, err)
	}
	return data, nil
}
