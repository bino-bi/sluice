// SPDX-License-Identifier: AGPL-3.0-or-later

package version

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCurrentDefaults(t *testing.T) {
	b := Current()

	if b.Version == "" {
		t.Fatal("Version should never be empty")
	}
	if b.Commit == "" {
		t.Fatal("Commit should never be empty")
	}
	if b.CommitFull == "" {
		t.Fatal("CommitFull should never be empty")
	}
	if b.GoVersion != runtime.Version() {
		t.Fatalf("GoVersion = %q, want %q", b.GoVersion, runtime.Version())
	}
	if got, want := b.Platform, runtime.GOOS+"/"+runtime.GOARCH; got != want {
		t.Fatalf("Platform = %q, want %q", got, want)
	}
	if b.Parser == "" {
		t.Fatal("Parser should never be empty")
	}
}

func TestBuildShort(t *testing.T) {
	b := Build{Version: "1.2.3"}
	if got := b.Short(); got != "1.2.3" {
		t.Fatalf("Short() = %q, want %q", got, "1.2.3")
	}
}

func TestBuildString(t *testing.T) {
	b := Build{
		Version:   "0.1.0",
		Commit:    "abcdef0",
		Dirty:     false,
		BuildTime: time.Date(2026, 4, 19, 14, 23, 45, 0, time.UTC),
		GoVersion: "go1.23.0",
		Platform:  "linux/amd64",
		Parser:    "pg_query",
	}
	want := "sluice 0.1.0 (abcdef0, dirty=false) built 2026-04-19T14:23:45Z go1.23.0 linux/amd64 parser=pg_query"
	if got := b.String(); got != want {
		t.Fatalf("String()\n got:  %q\n want: %q", got, want)
	}
}

func TestBuildStringDirty(t *testing.T) {
	b := Build{
		Version: "0.1.0", Commit: "abc", Dirty: true,
		BuildTime: time.Unix(0, 0).UTC(), GoVersion: "go1.23", Platform: "linux/amd64", Parser: "pg_query",
	}
	if !strings.Contains(b.String(), "dirty=true") {
		t.Fatalf("expected dirty=true in %q", b.String())
	}
}

func TestParseBool(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"true", true},
		{"1", true},
		{"yes", true},
		{"false", false},
		{"", false},
		{"no", false},
	} {
		if got := parseBool(tc.in); got != tc.want {
			t.Errorf("parseBool(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseBuildTime(t *testing.T) {
	rfc := "2026-04-19T14:23:45Z"
	if got := parseBuildTime(rfc); got.Format(time.RFC3339) != rfc {
		t.Errorf("parseBuildTime(%q) = %v", rfc, got)
	}

	if got := parseBuildTime("garbage"); !got.Equal(time.Unix(0, 0).UTC()) {
		t.Errorf("garbage should fall back to unix epoch; got %v", got)
	}
}

func TestShortCommit(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"a1b2c3d4e5f6", "a1b2c3d"},
		{"abc", "abc"},
		{"", ""},
	} {
		if got := shortCommit(tc.in); got != tc.want {
			t.Errorf("shortCommit(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestPgQueryVersion(t *testing.T) {
	if PgQueryVersion() == "" {
		t.Fatal("PgQueryVersion must not be empty")
	}
}
