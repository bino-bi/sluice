// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"runtime"
	"testing"
)

// goldenContentHash is the content hash of the baseSpec build with
// testSeed. Later changes must not move the output of a spec that uses
// no new feature, else built snapshots turn stale after an upgrade. A
// DuckDB upgrade can move it: 2.0 rounds avg and stddev_samp differently.
const goldenContentHash = "sha256:8c4f38d3468542e6715e056eec04f2b638c5e8c6961adb48ecd6b218e01b7f8a"

func TestGoldenContentHash(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("the content hash is pinned on amd64; math.Exp and math.Log may differ in the last bit elsewhere")
	}
	rep, _ := shared(t)
	if rep.ContentHash != goldenContentHash {
		t.Fatalf("content hash = %s, want %s", rep.ContentHash, goldenContentHash)
	}
}
