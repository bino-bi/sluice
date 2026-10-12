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
	"unicode/utf8"

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

// DuckDB quotes values of the statement in its messages. With
// MaxErrorBytes the handler cuts the text of an "error" attribute, so a
// caller does not decide how long a log line gets. 0 is no cut.
func TestNewSlogHandler_CutsErrorText(t *testing.T) {
	const limit = 4096
	// 3-byte characters after a 44-byte prefix: the cut lands inside one.
	long := "Conversion Error: Could not convert string '" + strings.Repeat("\u20ac", 100000) + "' to INT32"
	logged := func(limit int, attr slog.Attr) string {
		t.Helper()
		var buf bytes.Buffer
		NewLogger(&buf, LoggingConfig{Format: "json", MaxErrorBytes: limit}).Warn("failed", attr)
		var record map[string]string
		if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &record); err != nil {
			t.Fatalf("output is not JSON: %v", err)
		}
		return record[attr.Key]
	}

	for name, attr := range map[string]slog.Attr{
		"string": slog.String("error", long),
		"error":  slog.Any("error", errors.New(long)),
	} {
		got := logged(limit, attr)
		if len(got) > limit+64 || !strings.HasPrefix(got, long[:44]) || !strings.HasSuffix(got, "more bytes)") {
			t.Errorf("%s: logged %d bytes starting %q, want the text cut near %d with a note", name, len(got), got[:60], limit)
		}
		// The JSON handler writes a broken character as U+FFFD.
		if strings.ContainsRune(got, utf8.RuneError) {
			t.Errorf("%s: the cut split a character", name)
		}
	}
	if got := logged(limit, slog.String("error", "Binder Error: no column x")); got != "Binder Error: no column x" {
		t.Errorf("short error = %q, want it as it is", got)
	}
	if got := logged(0, slog.String("error", long)); got != long {
		t.Errorf("limit 0: logged %d bytes, want all %d", len(got), len(long))
	}
	if got := logged(limit, slog.String("detail", long)); got != long {
		t.Errorf("other attribute: logged %d bytes, want all %d", len(got), len(long))
	}
}

// An ERR_INTERNAL is logged with its query_id and its cause. Any other
// code is the client's to read and logs nothing.
func TestLogInternal(t *testing.T) {
	cause := errors.New("executor: query execution failed: IO Error: disk")
	cases := []struct {
		name string
		err  error
		want []string // nil: no log line
	}{
		{"wrapped cause", pkgerrors.Wrap(pkgerrors.CodeInternal, cause).WithQueryID("q-1"), []string{`"level":"ERROR"`, `"query_id":"q-1"`, "IO Error: disk"}},
		{"message only", pkgerrors.New(pkgerrors.CodeInternal).WithMessage("no broker").WithQueryID("q-2"), []string{`"query_id":"q-2"`, "ERR_INTERNAL: no broker"}},
		{"plain error", cause, []string{`"level":"ERROR"`, "IO Error: disk"}},
		{"long cause", pkgerrors.Wrap(pkgerrors.CodeInternal, errors.New("IO Error: disk "+strings.Repeat("x", 100000))), []string{"IO Error: disk", "more bytes"}},
		{"other code", pkgerrors.Wrap(pkgerrors.CodeQueryFailed, cause).WithQueryID("q-3"), nil},
		{"nil", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			LogInternal(context.Background(), NewLogger(&buf, LoggingConfig{Format: "json", Level: slog.LevelInfo, MaxErrorBytes: 4096}), tc.err)
			got := buf.String()
			if tc.want == nil && got != "" {
				t.Fatalf("log = %q, want nothing", got)
			}
			if len(got) > 8192 {
				t.Fatalf("log line has %d bytes, want the cause cut by the handler", len(got))
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Fatalf("log = %q, want it to contain %q", got, w)
				}
			}
		})
	}
}
