// Package logging constructs the structured slog.Logger used across every Pulse
// service, keeping handler selection and formatting consistent platform-wide.
package logging

import (
	"log/slog"
	"os"
)

// New returns a slog.Logger writing to stderr at the given minimum level.
// Production uses a JSON handler for machine ingestion; other environments use
// a human-readable text handler.
func New(level slog.Level, production bool) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if production {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		handler = slog.NewTextHandler(os.Stderr, opts)
	}
	return slog.New(handler)
}
