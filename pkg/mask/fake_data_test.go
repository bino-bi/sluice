// SPDX-License-Identifier: Apache-2.0

package mask

import "testing"

func TestFakeWords(t *testing.T) {
	for _, kind := range []string{"first_name", "last_name", "company", "city", "country"} {
		if len(FakeWords(kind)) == 0 {
			t.Errorf("%s: empty list", kind)
		}
	}
	for _, kind := range []string{"", "email", "street"} {
		if FakeWords(kind) != nil {
			t.Errorf("%s: want nil", kind)
		}
	}
	w := FakeWords("city")
	w[0] = "changed"
	if FakeWords("city")[0] == "changed" {
		t.Error("FakeWords returns the shared list, not a copy")
	}
}
