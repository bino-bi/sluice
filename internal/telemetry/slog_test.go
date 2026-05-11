// SPDX-License-Identifier: AGPL-3.0-or-later

package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	pkgerrors "github.com/bino-bi/sluice/pkg/errors"
)

func TestNewSlogHandler_JSON(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, LoggingConfig{Format: "json", Level: slog.LevelInfo})
	logger.Info("hello", slog.String("k", "v"))

	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &record); err != nil {
		t.Fatalf("output is not JSON: %v — raw: %s", err, buf.String())
	}
	if record["msg"] != "hello" {
		t.Fatalf("msg = %v, want %q", record["msg"], "hello")
	}
	if record["k"] != "v" {
		t.Fatalf("k = %v, want %q", record["k"], "v")
	}
}

func TestNewSlogHandler_Text(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, LoggingConfig{Format: "text", Level: slog.LevelInfo})
	logger.Info("hello")

	// Text handler emits key=val pairs; just confirm it's not JSON.
	out := buf.String()
	if strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Fatalf("text handler produced JSON-looking output: %q", out)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("text handler dropped message: %q", out)
	}
}

func TestAttrQueryID(t *testing.T) {
	a := AttrQueryID("qid-1")
	if a.Key != "query_id" || a.Value.String() != "qid-1" {
		t.Fatalf("AttrQueryID = %v", a)
	}
}

func TestAttrError_APIError(t *testing.T) {
	err := pkgerrors.Newf(pkgerrors.CodeACLDenied, "subject not allowed")
	a := AttrError(err)

	var buf bytes.Buffer
	logger := NewLogger(&buf, LoggingConfig{Format: "json"})
	logger.Info("denied", a)

	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &record); err != nil {
		t.Fatalf("json: %v", err)
	}
	errGroup, ok := record["error"].(map[string]any)
	if !ok {
		t.Fatalf("error attr not a group: %v", record)
	}
	if errGroup["code"] != string(pkgerrors.CodeACLDenied) {
		t.Fatalf("code = %v, want %q", errGroup["code"], pkgerrors.CodeACLDenied)
	}
	if errGroup["message"] != "subject not allowed" {
		t.Fatalf("message = %v", errGroup["message"])
	}
}

func TestAttrError_Plain(t *testing.T) {
	a := AttrError(errors.New("boom"))
	var buf bytes.Buffer
	logger := NewLogger(&buf, LoggingConfig{Format: "json"})
	logger.Info("oops", a)

	if !strings.Contains(buf.String(), `"message":"boom"`) {
		t.Fatalf("want plain error message in output, got: %s", buf.String())
	}
}

func TestAttrError_Nil(t *testing.T) {
	a := AttrError(nil)
	if a.Key != "error" {
		t.Fatalf("nil error should still produce an error group, got %v", a)
	}
}

func TestRedacted_DoesNotLeakValue(t *testing.T) {
	secret := "hunter2"
	var buf bytes.Buffer
	logger := NewLogger(&buf, LoggingConfig{Format: "json"})
	logger.InfoContext(context.Background(), "auth", slog.Any("pepper", Redacted{Value: secret}))

	out := buf.String()
	if strings.Contains(out, secret) {
		t.Fatalf("secret leaked: %s", out)
	}
	if !strings.Contains(out, "[redacted]") {
		t.Fatalf("expected [redacted] marker in output: %s", out)
	}
}
