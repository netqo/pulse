package logging

import (
	"context"
	"log/slog"
	"testing"
)

func TestNewRespectsLevel(t *testing.T) {
	tests := map[string]struct {
		configured slog.Level
		probe      slog.Level
		wantOn     bool
	}{
		"below threshold is suppressed": {slog.LevelInfo, slog.LevelDebug, false},
		"at threshold is emitted":       {slog.LevelInfo, slog.LevelInfo, true},
		"above threshold is emitted":    {slog.LevelWarn, slog.LevelError, true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			logger := New(tc.configured, false)
			if logger == nil {
				t.Fatal("New returned nil logger")
			}
			if got := logger.Enabled(context.Background(), tc.probe); got != tc.wantOn {
				t.Errorf("Enabled(%v) = %v, want %v", tc.probe, got, tc.wantOn)
			}
		})
	}
}

func TestNewProductionHandler(t *testing.T) {
	if New(slog.LevelInfo, true) == nil {
		t.Fatal("New(production) returned nil logger")
	}
}
