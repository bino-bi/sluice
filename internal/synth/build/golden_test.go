// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"runtime"
	"testing"
)

// goldenContentHash is the content hash of the baseSpec build with
// testSeed. Later changes must not move the output of a spec that uses
// no new feature, else built snapshots turn stale after an upgrade.
const goldenContentHash = "sha256:60f0ce57e77c7f20b037cbbe3e2faa11ac965fb3270c32fd09a2dbfc14693425"

func TestGoldenContentHash(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("the content hash is pinned on amd64; math.Exp and math.Log may differ in the last bit elsewhere")
	}
	rep, _ := shared(t)
	if rep.ContentHash != goldenContentHash {
		t.Fatalf("content hash = %s, want %s", rep.ContentHash, goldenContentHash)
	}
}
