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
// The default range at the k-th value from each end moved it on purpose.
const goldenContentHash = "sha256:6e47d71e244824b52287af2129c1be8840151b1ffb232a6c3bde289f3c72ca12"

func TestGoldenContentHash(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("the content hash is pinned on amd64; math.Exp and math.Log may differ in the last bit elsewhere")
	}
	rep, _ := shared(t)
	if rep.ContentHash != goldenContentHash {
		t.Fatalf("content hash = %s, want %s", rep.ContentHash, goldenContentHash)
	}
}
