// Package logging provides the shared slog-based logger for the Cairn server.
package logging

import (
	"log/slog"
	"os"
)

// New creates a leveled slog logger writing JSON to stderr.
//
// Structured logs go to stderr so that stdout stays clean for any future
// process output and so container log drivers receive application logs as a
// unified stream.
func New(level string) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: parseLevel(level),
	}))
}

// parseLevel maps a config log level string to a slog.Level. Unknown values
// fall back to Info; config.Validate rejects them before New is called.
func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
