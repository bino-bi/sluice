// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bino-bi/sluice/pkg/apitypes"
)

// Snapshot is the immutable output of a directory load. An empty directory
// yields a valid Snapshot (default-deny is enforced by downstream packages
// that interpret "no matching policy" as deny).
type Snapshot struct {
	Version  int64
	LoadedAt time.Time
	Digest   string

	Policies        []apitypes.Object
	ByKind          map[apitypes.Kind][]apitypes.Object
	DataSources     []*apitypes.DataSource
	SubjectBindings []*apitypes.SubjectBinding
	AuditSinks      []*apitypes.AuditSink

	Warnings []ValidationWarning
}

// ValidationWarning is a non-fatal issue (e.g. unknown fields in non-strict
// mode) surfaced alongside a successful snapshot.
type ValidationWarning struct {
	File string
	Line int
	Msg  string
}

// LoadOptions configures LoadDirectory. Zero values produce the MVP default:
// non-strict decode, one source directory, built-in type registry.
type LoadOptions struct {
	Strict   bool
	Sources  []SourceDir
	Registry apitypes.TypeRegistry
}

// SourceDir is one YAML source directory. Multiple directories are merged
// into a single Snapshot.
type SourceDir struct {
	Path           string
	FollowSymlinks bool
}

// LoadDirectory reads every *.yaml / *.yml file under opts.Sources, decodes
// them via apitypes.Decode, and assembles a Snapshot. On any validation
// failure the combined ValidationErrors is returned and the Snapshot is nil.
//
// An empty or missing directory is not an error: LoadDirectory returns an
// empty Snapshot so the default-deny posture is explicit.
func LoadDirectory(_ context.Context, opts LoadOptions) (*Snapshot, error) {
	if opts.Registry == nil {
		opts.Registry = apitypes.DefaultRegistry()
	}

	files, err := collectFiles(opts.Sources)
	if err != nil {
		return nil, err
	}

	snap := &Snapshot{
		LoadedAt: time.Now().UTC(),
		ByKind:   map[apitypes.Kind][]apitypes.Object{},
	}

	var verrs ValidationErrors
	h := sha256.New()
	seen := map[string]string{} // kind/name → file

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("config: read %s: %w", file, err)
		}
		h.Write(data)

		decodeOpts := []apitypes.DecodeOption{apitypes.WithRegistry(opts.Registry)}
		if opts.Strict {
			decodeOpts = append(decodeOpts, apitypes.WithStrictUnknown(true))
		}

		objs, derr := apitypes.Decode(strings.NewReader(string(data)), decodeOpts...)
		if derr != nil {
			ve := toValidationError(file, derr)
			verrs = append(verrs, ve)
			// Still keep partially-decoded objects so multi-doc files with
			// one bad document can surface both the failure and any
			// further-document issues on re-run.
		}

		for _, obj := range objs {
			meta := obj.GetObjectMeta()
			kind := obj.GetKind()
			key := string(kind) + "/" + meta.Name
			if existing, dup := seen[key]; dup {
				verrs = append(verrs, &ValidationError{
					File: file,
					Kind: kind,
					Name: meta.Name,
					Msg:  fmt.Sprintf("duplicate %s/%s also declared in %s", kind, meta.Name, existing),
				})
				continue
			}
			seen[key] = file

			snap.Policies = append(snap.Policies, obj)
			snap.ByKind[kind] = append(snap.ByKind[kind], obj)
			switch x := obj.(type) {
			case *apitypes.DataSource:
				snap.DataSources = append(snap.DataSources, x)
			case *apitypes.SubjectBinding:
				snap.SubjectBindings = append(snap.SubjectBindings, x)
			case *apitypes.AuditSink:
				snap.AuditSinks = append(snap.AuditSinks, x)
			}
		}
	}

	if len(verrs) > 0 {
		return nil, verrs
	}

	snap.Digest = hex.EncodeToString(h.Sum(nil))
	return snap, nil
}

// collectFiles walks every source directory and returns sorted absolute file
// paths for *.yaml / *.yml. Missing directories are treated as empty so
// LoadDirectory can return an empty snapshot.
func collectFiles(sources []SourceDir) ([]string, error) {
	var files []string
	for _, src := range sources {
		if src.Path == "" {
			continue
		}
		info, err := os.Stat(src.Path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("config: stat %s: %w", src.Path, err)
		}
		if !info.IsDir() {
			files = append(files, src.Path)
			continue
		}

		err = filepath.WalkDir(src.Path, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				// Skip hidden directories (.git, .hidden), editor noise, and
				// a `tests` subdirectory, which by convention holds policy
				// test suites (policytest), not policy manifests.
				name := d.Name()
				if path != src.Path && (strings.HasPrefix(name, ".") || name == "testdata" || name == "tests") {
					return fs.SkipDir
				}
				return nil
			}
			if !isYAML(path) {
				return nil
			}
			files = append(files, path)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("config: walk %s: %w", src.Path, err)
		}
	}
	sort.Strings(files)
	return files, nil
}

func isYAML(p string) bool {
	ext := strings.ToLower(filepath.Ext(p))
	return ext == ".yaml" || ext == ".yml"
}

// toValidationError normalizes an apitypes decode error into a ValidationError.
// The underlying apitypes.ValidationError carries Kind/Name/Field/Reason; the
// file context is added here.
func toValidationError(file string, err error) *ValidationError {
	ve := &ValidationError{File: file, Msg: err.Error(), Cause: err}
	var apiErr *apitypes.ValidationError
	if errors.As(err, &apiErr) {
		ve.Kind = apiErr.Kind
		ve.Name = apiErr.Name
		ve.Line = apiErr.Line
		msg := apiErr.Reason
		if apiErr.Field != "" {
			msg = apiErr.Field + ": " + msg
		}
		ve.Msg = msg
	}
	return ve
}
