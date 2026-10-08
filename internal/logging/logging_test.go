package logging

import (
	"log/slog"
	"testing"
)

func TestNew(t *testing.T) {
	for _, level := range []string{"debug", "info", "warn", "error"} {
		if logger := New(level); logger == nil {
			t.Fatalf("New(%q) returned nil logger", level)
		}
	}
}

func TestParseLevel(t *testing.T) {
	cases := []struct {
		in   string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
		{"bogus", slog.LevelInfo},
		{"", slog.LevelInfo},
	}
	for _, tc := range cases {
		if got := parseLevel(tc.in); got != tc.want {
			t.Errorf("parseLevel(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
