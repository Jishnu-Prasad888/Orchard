package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// clearEnv removes every CAIRN_* variable from the environment so tests do
// not depend on the developer's shell.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, EnvPrefix+"_") {
			continue
		}
		name := kv[:strings.IndexByte(kv, '=')]
		t.Setenv(name, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)
	t.Setenv("HOME", "/home/pebble")

	cfg := Load()

	if cfg.HTTPAddr != DefaultHTTPAddr {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, DefaultHTTPAddr)
	}
	if cfg.LogLevel != DefaultLogLevel {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, DefaultLogLevel)
	}
	wantDataDir := filepath.Join("/home/pebble", ".local", "share", "cairn")
	if cfg.DataDir != wantDataDir {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, wantDataDir)
	}
	if cfg.WebDistDir != "" {
		t.Errorf("WebDistDir = %q, want empty", cfg.WebDistDir)
	}
}

func TestLoadXDGDataHome(t *testing.T) {
	clearEnv(t)
	t.Setenv("HOME", "/home/pebble")
	t.Setenv("XDG_DATA_HOME", "/var/data")

	if got := Load().DataDir; got != filepath.Join("/var/data", "cairn") {
		t.Errorf("DataDir with XDG_DATA_HOME = %q", got)
	}
}

func TestLoadEnvOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("CAIRN_HTTP_ADDR", "0.0.0.0:9999")
	t.Setenv("CAIRN_DATA_DIR", "/srv/cairn-state")
	t.Setenv("CAIRN_LOG_LEVEL", "debug")
	t.Setenv("CAIRN_ENCRYPTION_PASSPHRASE", "hunter2-at-rest")

	cfg := Load()
	if cfg.HTTPAddr != "0.0.0.0:9999" {
		t.Errorf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.DataDir != "/srv/cairn-state" {
		t.Errorf("DataDir = %q", cfg.DataDir)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q", cfg.LogLevel)
	}
	if cfg.EncryptionPassphrase != "hunter2-at-rest" {
		t.Errorf("EncryptionPassphrase = %q", cfg.EncryptionPassphrase)
	}
}

func TestLoadTrimsWhitespace(t *testing.T) {
	clearEnv(t)
	t.Setenv("CAIRN_LOG_LEVEL", "  warn  ")

	if got := Load().LogLevel; got != "warn" {
		t.Errorf("LogLevel = %q, want %q", got, "warn")
	}
}

func TestValidate(t *testing.T) {
	base := Config{HTTPAddr: ":8080", DataDir: "/tmp/x", LogLevel: "info"}

	if err := base.Validate(); err != nil {
		t.Errorf("unexpected validation error: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"empty HTTPAddr", func(c *Config) { c.HTTPAddr = "" }},
		{"empty DataDir", func(c *Config) { c.DataDir = "" }},
		{"invalid log level", func(c *Config) { c.LogLevel = "loud" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			tc.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Error("expected validation error")
			}
		})
	}
}

func TestDatabasePath(t *testing.T) {
	cfg := Config{DataDir: "/tmp/state"}
	if got := cfg.DatabasePath(); got != filepath.Join("/tmp/state", "cairn.db") {
		t.Errorf("DatabasePath = %q", got)
	}
}
