// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/parserbackend"
)

// A setting that is not copied onto the logger at boot has no effect. The
// default cut of a logged error text and its off switch must reach the
// logger buildRuntime installs.
func TestBuildRuntime_LoggedErrorCut(t *testing.T) {
	if !parserbackend.Implemented {
		t.Skip("buildRuntime refuses the stub parser backend (-tags=pure_parser)")
	}
	long := strings.Repeat("A", 100000)
	cases := []struct {
		name, logging string
		cut           bool
	}{
		{"default", "", true},
		{"switched off", "logging:\n  maxErrorBytes: 0\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			policyDir := filepath.Join(dir, "policies.d")
			if err := os.MkdirAll(policyDir, 0o750); err != nil {
				t.Fatalf("mkdir policies: %v", err)
			}
			cfgPath := filepath.Join(dir, "sluice.yaml")
			cfg := "policies:\n  dir: " + policyDir + "\naudit:\n  file:\n    path: " + filepath.Join(dir, "audit") + "\n" + tc.logging
			if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}

			// buildRuntime installs a logger that writes to os.Stderr.
			logPath := filepath.Join(dir, "stderr.log")
			logFile, err := os.Create(logPath)
			if err != nil {
				t.Fatalf("create log file: %v", err)
			}
			stderr, logger := os.Stderr, slog.Default()
			os.Stderr = logFile
			t.Cleanup(func() {
				os.Stderr = stderr
				slog.SetDefault(logger)
				_ = logFile.Close()
			})

			deps, err := buildRuntime(context.Background(), cfgPath, policyDir)
			if err != nil {
				t.Fatalf("buildRuntime: %v", err)
			}
			t.Cleanup(func() {
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				deps.Close(shutdownCtx)
			})

			deps.log.Warn("probe", "error", long)
			out, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatalf("read log file: %v", err)
			}
			if !strings.Contains(string(out), `"msg":"probe"`) {
				t.Fatalf("log = %q, want the probe line", out)
			}
			if cut := !strings.Contains(string(out), long); cut != tc.cut {
				t.Fatalf("error text cut = %v, want %v", cut, tc.cut)
			}
		})
	}
}
