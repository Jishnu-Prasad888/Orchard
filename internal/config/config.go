// Package config loads Cairn server configuration from the environment.
//
// Configuration is intentionally environment-driven. A single self-hosted
// process should not require a config file to run; advanced options can be
// added later without breaking the base contract.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// EnvPrefix is the prefix used for all Cairn environment variables.
	EnvPrefix = "CAIRN"

	// DefaultHTTPAddr is the default listen address.
	DefaultHTTPAddr = "127.0.0.1:8715"

	// DefaultLogLevel is the default log verbosity.
	DefaultLogLevel = "info"
)

// Config holds the runtime configuration for the Cairn server process.
type Config struct {
	// HTTPAddr is the listen address for the HTTP/JSON API and web UI.
	HTTPAddr string

	// DataDir is the server-level data directory. It holds the server
	// database and other server-scoped state. Library metadata lives inside
	// each library, never here.
	DataDir string

	// LogLevel is one of debug, info, warn, error.
	LogLevel string

	// WebDistDir overrides the embedded frontend with a directory of built
	// static assets. Empty means "use the embedded frontend". Useful for
	// development and for embedding tools that cannot use go:embed.
	WebDistDir string

	// CookieSecure forces the Secure flag on the session cookie. When false,
	// the flag is still set automatically for HTTPS requests observed by the
	// server. Set to true when Cairn sits behind a TLS-terminating reverse
	// proxy.
	CookieSecure bool

	// MaxUploadBytes caps the size of a single upload body in bytes (Phase 16
	// hardening). Zero falls back to media.DefaultMaxUploadBytes (2 GiB).
	MaxUploadBytes int64

	// ScanIntervalSeconds is how often every online library is rescanned so
	// changes made outside Cairn (a file manager, a sync tool) appear on their
	// own. Zero or negative disables the automatic rescan.
	ScanIntervalSeconds int

	// BackupDir is where library and server backups are written. Empty means
	// backups are disabled until configured.
	BackupDir string

	// BackupKeep is how many completed backups are retained. Older backups are
	// pruned automatically after each successful backup.
	BackupKeep int

	// BackupIntervalMinutes controls the scheduled backup cadence. Zero or
	// negative disables the scheduler (manual backups still work).
	BackupIntervalMinutes int

	// BackupPassphrase optionally encrypts every backup payload with the
	// shared AEAD kernel (AES-256-GCM, Phase 14). The passphrase is never
	// stored; a random per-backup salt is derived with argon2id. Backup
	// restore requires the same passphrase.
	BackupPassphrase string

	// EncryptionPassphrase optionally encrypts Cairn-owned metadata at rest:
	// each library's .cairn/library.json identity file and generated
	// thumbnails are sealed with AES-256-GCM under this passphrase. The
	// passphrase is never stored; the key is derived with argon2id using a
	// fixed protocol salt. Empty disables encryption. User media files are
	// never modified (see ADR-0004).
	EncryptionPassphrase string

	// MLEnabled is the master switch for local ML capabilities. All ML work
	// is off and nothing is stored until this is true (default false).
	MLEnabled bool

	// MLSimilarity turns the perceptual similarity capability on. It is only
	// consulted when MLEnabled is true.
	MLSimilarity bool

	// MLWorkers caps concurrent files processed per similarity pass.
	MLWorkers int

	// MLDistanceThreshold is the maximum Hamming distance (0-64) at or below
	// which files are reported as similar.
	MLDistanceThreshold int

	// MLFaces turns the local face capability on (detection, clustering, and
	// people). It is only consulted when MLEnabled is true; default false.
	MLFaces bool

	// MLFaceWorkers caps concurrent files scanned per face pass.
	MLFaceWorkers int

	// MLFaceMinConfidence floors a detection's normalized score (0..1).
	// pigo reports raw scores around 5..30 for real faces, so 0.05 maps to
	// that same raw threshold.
	MLFaceMinConfidence float64

	// MLFaceMinSize is the smallest face (width and height, source pixels) kept.
	MLFaceMinSize int

	// MLFaceThreshold is the minimum cosine similarity (0..1) for an
	// unassigned face to join an existing person during clustering.
	MLFaceThreshold float64

	// MLFaceModel is the path of the ONNX face-recognition (ArcFace) model. When the
	// file exists, faces are matched with its embeddings; otherwise face
	// recognition is unavailable until the model is downloaded.
	MLFaceModel string

	// MLFaceModelURL is where the ArcFace recognizer model is downloaded from on request.
	MLFaceModelURL string

	// MLFaceDetectorModel is the path of the SCRFD ONNX face-detector model.
	// When both this and MLFaceModel are present, the SCRFD+ArcFace pipeline
	// is used. When absent, face recognition is unavailable until the model
	// is downloaded.
	MLFaceDetectorModel string

	// MLFaceDetectorURL is where the SCRFD detector model is downloaded from on request.
	MLFaceDetectorURL string
}

// Load builds a Config from the process environment and platform defaults.
//
// Cairn settings follow the CAIRN_<NAME> naming scheme, e.g. CAIRN_HTTP_ADDR,
// CAIRN_DATA_DIR. Standard platform variables (XDG_DATA_HOME, HOME) are
// honored for the default data directory.
func Load() Config {
	return Config{
		HTTPAddr:              envOrDefault("HTTP_ADDR", DefaultHTTPAddr),
		DataDir:               dataDir(),
		LogLevel:              envOrDefaultTrim("LOG_LEVEL", DefaultLogLevel),
		WebDistDir:            envOrDefaultTrim("WEB_DIST", ""),
		CookieSecure:          envOrDefaultBool("COOKIE_SECURE", false),
		MaxUploadBytes:        envOrDefaultInt64("MAX_UPLOAD_BYTES", 2<<30),
		ScanIntervalSeconds:   envOrDefaultInt("SCAN_INTERVAL_SEC", 60),
		BackupDir:             envOrDefaultTrim("BACKUP_DIR", ""),
		BackupKeep:            envOrDefaultInt("BACKUP_KEEP", 4),
		BackupIntervalMinutes: envOrDefaultInt("BACKUP_INTERVAL_MIN", 0),
		BackupPassphrase:      os.Getenv(EnvPrefix + "_BACKUP_PASSPHRASE"),
		EncryptionPassphrase:  os.Getenv(EnvPrefix + "_ENCRYPTION_PASSPHRASE"),
		MLEnabled:             envOrDefaultBool("ML_ENABLED", false),
		MLSimilarity:          envOrDefaultBool("ML_SIMILARITY", true),
		MLWorkers:             envOrDefaultInt("ML_WORKERS", 2),
		MLDistanceThreshold:   envOrDefaultInt("ML_DISTANCE_THRESHOLD", 10),
		MLFaces:               envOrDefaultBool("ML_FACES", false),
		MLFaceWorkers:         envOrDefaultInt("ML_FACE_WORKERS", 2),
		MLFaceMinConfidence:   envOrDefaultFloat("ML_FACE_MIN_CONFIDENCE", 0.05),
		MLFaceMinSize:         envOrDefaultInt("ML_FACE_MIN_SIZE", 48),
		MLFaceThreshold:       envOrDefaultFloat("ML_FACE_THRESHOLD", 0),
		MLFaceModel:           envOrDefault("ML_FACE_MODEL", ""),
		MLFaceModelURL:        envOrDefault("ML_FACE_MODEL_URL", ""),
		MLFaceDetectorModel:   envOrDefault("ML_FACE_DETECTOR_MODEL", ""),
		MLFaceDetectorURL:     envOrDefault("ML_FACE_DETECTOR_URL", ""),
	}
}

// Validate returns an error if the configuration is not usable.
func (c Config) Validate() error {
	if c.HTTPAddr == "" {
		return errors.New("HTTPAddr must not be empty")
	}
	if c.DataDir == "" {
		return errors.New("DataDir must not be empty")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("invalid LogLevel %q: must be one of debug, info, warn, error", c.LogLevel)
	}
	return nil
}

// DatabasePath returns the path of the server-level SQLite database file.
func (c Config) DatabasePath() string {
	return filepath.Join(c.DataDir, "cairn.db")
}

// dataDir resolves the default data directory, honoring the CAIRN_DATA_DIR
// environment variable, then XDG_DATA_HOME and HOME on Unix-like systems. It
// returns a relative fallback only as a last resort so that unrelated files
// are never created in the working tree.
func dataDir() string {
	if v := envOrDefaultTrim("DATA_DIR", ""); v != "" {
		return v
	}

	if xdg := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); xdg != "" {
		return filepath.Join(xdg, "cairn")
	}

	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		return filepath.Join(home, ".local", "share", "cairn")
	}

	return filepath.Join("cairn-data")
}

// envOrDefault returns the value of the CAIRN_<name> environment variable or
// the given default. Whitespace is preserved.
func envOrDefault(name, def string) string {
	if v, ok := os.LookupEnv(EnvPrefix + "_" + name); ok && v != "" {
		return v
	}
	return def
}

// envOrDefaultTrim is like envOrDefault but trims surrounding whitespace.
func envOrDefaultTrim(name, def string) string {
	if v, ok := os.LookupEnv(EnvPrefix + "_" + name); ok {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return def
}

// envOrDefaultBool parses a CAIRN_<name> boolean setting. Only the literal
// values accepted by strconv.ParseBool are honored; anything else is treated
// as an explicit "false" so a malformed value never silently enables a
// security flag.
func envOrDefaultBool(name string, def bool) bool {
	if v, ok := os.LookupEnv(EnvPrefix + "_" + name); ok {
		if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			return b
		}
	}
	return def
}

// envOrDefaultInt parses a CAIRN_<name> integer setting. Only valid integers
// are honored; anything else falls back to the default so a malformed value
// never disables an outage-prone safety setting.
func envOrDefaultInt(name string, def int) int {
	if v, ok := os.LookupEnv(EnvPrefix + "_" + name); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

// envOrDefaultFloat parses a CAIRN_<name> floating-point setting, falling
// back to the default when unset or malformed.
func envOrDefaultFloat(name string, def float64) float64 {
	if v, ok := os.LookupEnv(EnvPrefix + "_" + name); ok {
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f
		}
	}
	return def
}

// envOrDefaultInt64 parses a CAIRN_<name> 64-bit integer setting, falling
// back to the default when unset or malformed.
func envOrDefaultInt64(name string, def int64) int64 {
	if v, ok := os.LookupEnv(EnvPrefix + "_" + name); ok {
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return n
		}
	}
	return def
}
