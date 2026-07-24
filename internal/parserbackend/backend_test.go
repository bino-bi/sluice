// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !pure_parser

package parserbackend

import (
	"context"
	"testing"

	"github.com/bino-bi/sluice/internal/parser"
)

func TestDefaultBackendImplemented(t *testing.T) {
	if !Implemented {
		t.Fatal("default backend must report Implemented = true")
	}
	p := New(parser.Options{})
	if _, err := p.Parse(context.Background(), "SELECT 1"); err != nil {
		t.Fatalf("Parse(SELECT 1) failed on the default backend: %v", err)
	}
}
