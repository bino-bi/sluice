// SPDX-License-Identifier: AGPL-3.0-or-later

package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"sync"
	"time"
)

// Variables populated via -ldflags at build time. See Makefile / .goreleaser.yaml.
// Kept exported so `go build -ldflags "-X .../version.Version=..."` can write
// into them regardless of unexported-symbol linker behavior.
var (
	Version    = "dev"
	Commit     = "none"
	CommitFull = "none"
	Dirty      = "false"
	BuildTime  = "1970-01-01T00:00:00Z"
	Parser     = "pg_query"
)

// Build is the immutable build identity of the running binary. It is read by
// the CLI, health endpoints, audit records, Prometheus labels, and OTel
// resource attributes.
type Build struct {
	Version    string
	Commit     string
	CommitFull string
	Dirty      bool
	BuildTime  time.Time
	GoVersion  string
	Platform   string
	Parser     string
}

var (
	currentOnce sync.Once
	current     Build
)

// Current returns the build metadata for the running binary. Thread-safe and
// safe to call from package init. The returned value is a copy.
func Current() Build {
	currentOnce.Do(resolveCurrent)
	return current
}

func resolveCurrent() {
	current = Build{
		Version:    Version,
		Commit:     Commit,
		CommitFull: CommitFull,
		Dirty:      parseBool(Dirty),
		BuildTime:  parseBuildTime(BuildTime),
		GoVersion:  runtime.Version(),
		Platform:   runtime.GOOS + "/" + runtime.GOARCH,
		Parser:     Parser,
	}

	// debug.ReadBuildInfo fills in gaps when the binary was built without our
	// ldflags (e.g. `go run`, `go install`). ldflag-provided values win.
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if current.CommitFull == "none" && s.Value != "" {
				current.CommitFull = s.Value
				current.Commit = shortCommit(s.Value)
			}
		case "vcs.modified":
			if Dirty == "false" {
				current.Dirty = parseBool(s.Value)
			}
		case "vcs.time":
			if BuildTime == "1970-01-01T00:00:00Z" {
				if t, err := time.Parse(time.RFC3339, s.Value); err == nil {
					current.BuildTime = t
				}
			}
		}
	}
}

func parseBool(s string) bool {
	return s == "true" || s == "1" || s == "yes"
}

func parseBuildTime(s string) time.Time {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	// Fallbacks for non-RFC3339 strings goreleaser may emit.
	if t, err := time.Parse("2006-01-02T15:04:05Z", s); err == nil {
		return t
	}
	return time.Unix(0, 0).UTC()
}

func shortCommit(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// Short returns just the semantic version string, e.g. "0.1.0".
func (b Build) Short() string {
	return b.Version
}

// String returns a human-friendly one-liner suitable for `sluice version`.
// Format: "sluice 0.1.0 (a1b2c3d, dirty=false) built 2026-04-19T14:23:45Z go1.23 linux/amd64 parser=pg_query".
func (b Build) String() string {
	return fmt.Sprintf(
		"sluice %s (%s, dirty=%t) built %s %s %s parser=%s",
		b.Version,
		b.Commit,
		b.Dirty,
		b.BuildTime.UTC().Format(time.RFC3339),
		b.GoVersion,
		b.Platform,
		b.Parser,
	)
}
