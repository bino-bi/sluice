// SPDX-License-Identifier: AGPL-3.0-or-later

package executor

import (
	"slices"
	"testing"
)

func TestHardeningStatementsDefault(t *testing.T) {
	got := hardeningStatements(HardenConfig{})
	want := []string{
		"SET enable_external_access=false",
		"SET allow_community_extensions=false",
		"SET autoinstall_known_extensions=false",
		"SET autoload_known_extensions=false",
		"SET lock_configuration=true",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("statements = %q; want %q", got, want)
	}
}

func TestHardeningStatementsCarveOutOrderAndDedup(t *testing.T) {
	got := hardeningStatements(HardenConfig{
		AllowedExternalPaths: []string{"s3://bucket/", "http://host:1234/", "s3://bucket/", ""},
	})
	// The carve-out must precede enable_external_access=false, keep the
	// temp-spill entry first, and sort/dedupe the remote prefixes.
	wantFirst := "SET allowed_directories=['.tmp/', 'http://host:1234/', 's3://bucket/']"
	if got[0] != wantFirst {
		t.Fatalf("statements[0] = %q; want %q", got[0], wantFirst)
	}
	if got[1] != "SET enable_external_access=false" {
		t.Fatalf("statements[1] = %q; want the security floor next", got[1])
	}
}

func TestHardeningStatementsCarveOutTempDirAndEscaping(t *testing.T) {
	got := hardeningStatements(HardenConfig{
		TempDirectory:        "/mnt/spill",
		AllowedExternalPaths: []string{"s3://it's-a-bucket/"},
	})
	wantFirst := "SET allowed_directories=['/mnt/spill', 's3://it''s-a-bucket/']"
	if got[0] != wantFirst {
		t.Fatalf("statements[0] = %q; want %q", got[0], wantFirst)
	}
}
