// SPDX-License-Identifier: Apache-2.0

package errors

import (
	"net/http"
	"testing"
)

func TestAllCodesHaveStatusAndMessage(t *testing.T) {
	t.Parallel()
	for _, c := range AllCodes() {
		t.Run(string(c), func(t *testing.T) {
			t.Parallel()
			if _, ok := httpStatusByCode[c]; !ok {
				t.Errorf("code %q has no HTTP status mapping", c)
			}
			if _, ok := defaultMessage[c]; !ok {
				t.Errorf("code %q has no default message", c)
			}
			if got := Status(c); got == 0 {
				t.Errorf("code %q Status() returned 0", c)
			}
		})
	}
}

func TestStatusUnknownCodeIs500(t *testing.T) {
	t.Parallel()
	if got := Status(Code("not-a-real-code")); got != http.StatusInternalServerError {
		t.Errorf("unknown code should map to 500, got %d", got)
	}
}

func TestMessageUnknownCodeFallsBackToInternal(t *testing.T) {
	t.Parallel()
	if got := Message(Code("not-a-real-code")); got != defaultMessage[CodeInternal] {
		t.Errorf("unknown code should fall back to CodeInternal message, got %q", got)
	}
}

func TestEveryMappedCodeIsDeclared(t *testing.T) {
	t.Parallel()
	declared := make(map[Code]struct{}, len(AllCodes()))
	for _, c := range AllCodes() {
		declared[c] = struct{}{}
	}
	for c := range httpStatusByCode {
		if _, ok := declared[c]; !ok {
			t.Errorf("httpStatusByCode contains %q which is not in AllCodes()", c)
		}
	}
	for c := range defaultMessage {
		if _, ok := declared[c]; !ok {
			t.Errorf("defaultMessage contains %q which is not in AllCodes()", c)
		}
	}
}
