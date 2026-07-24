// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build pure_parser

package parserbackend

import (
	"context"
	"testing"

	"github.com/bino-bi/sluice/internal/parser"
)

func TestStubBackendNotImplemented(t *testing.T) {
	if Implemented {
		t.Fatal("pure_parser stub must report Implemented = false")
	}
	p := New(parser.Options{})
	if _, err := p.Parse(context.Background(), "SELECT 1"); err == nil {
		t.Fatal("stub Parse must error")
	}
	if _, err := p.Deparse(context.Background(), nil); err == nil {
		t.Fatal("stub Deparse must error")
	}
	if _, err := p.Fingerprint("SELECT 1"); err == nil {
		t.Fatal("stub Fingerprint must error")
	}
}
