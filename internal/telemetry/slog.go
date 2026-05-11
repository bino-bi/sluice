// SPDX-License-Identifier: AGPL-3.0-or-later

package telemetry

import (
	"errors"
	"io"
	"log/slog"
	"os"

	pkgerrors "github.com/bino-bi/sluice/pkg/errors"
)

// newSlogHandler builds a slog.Handler according to cfg. Output defaults to
// stderr so stdout stays clean for CLI-style data output.
func newSlogHandler(cfg LoggingConfig) slog.Handler {
	out := cfg.Output
	if out == nil {
		out = os.Stderr
	}
	opts := &slog.HandlerOptions{Level: cfg.Level}
	switch cfg.Format {
	case "text":
		return slog.NewTextHandler(out, opts)
	default:
		return slog.NewJSONHandler(out, opts)
	}
}

// NewLogger returns a logger with the given handler. Exposed for tests that
// want to inspect emitted records without going through the default logger.
func NewLogger(w io.Writer, cfg LoggingConfig) *slog.Logger {
	c := cfg
	c.Output = w
	return slog.New(newSlogHandler(c))
}

// AttrQueryID is a canonical slog attr for the query_id audit field.
func AttrQueryID(id string) slog.Attr {
	return slog.String("query_id", id)
}

// AttrError normalizes an error into a slog group. For pkg/errors.APIError it
// extracts the stable Code and user-facing Message; other errors surface only
// their Error() string. Nil is a no-op group.
func AttrError(err error) slog.Attr {
	if err == nil {
		return slog.Group("error")
	}
	var api *pkgerrors.APIError
	if errors.As(err, &api) {
		return slog.Group("error",
			slog.String("code", string(api.Code)),
			slog.String("message", api.Message),
		)
	}
	return slog.Group("error", slog.String("message", err.Error()))
}

// Redacted wraps a sensitive value so its content never reaches a slog handler.
// The wrapped value is replaced with the string "[redacted]" during record
// encoding; the original value is not retained in the log record.
type Redacted struct{ Value any }

// LogValue implements slog.LogValuer.
func (Redacted) LogValue() slog.Value {
	return slog.StringValue("[redacted]")
}
