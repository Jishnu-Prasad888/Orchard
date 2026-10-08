// Package usersettings stores per-user preferences in the server database.
//
// Preferences are key/value rows in user_settings (server migration 0007),
// scoped to one account so they follow the user across browsers and clients.
// Every preference has a documented default that applies until the user
// changes it; unknown keys are ignored when reading.
package usersettings

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Memory preference defaults.
const (
	DefaultSlideshowInterval = 15 // seconds
	MinSlideshowInterval     = 3
	MaxSlideshowInterval     = 3600
)

// Layouts and editor modes accepted by the Memories settings.
var (
	memoryLayouts = map[string]bool{"grid": true, "hero": true, "masonry": true,
		"two_column": true, "filmstrip": true, "featured": true}
	editorModes = map[string]bool{"edit": true, "preview": true}
)

// MemorySettings are the Memories section of Settings.
type MemorySettings struct {
	// SlideshowInterval is the automatic slideshow advance, in seconds.
	SlideshowInterval int `json:"slideshow_interval"`
	// EditedCopies creates a derived JPEG in .cairn/memory-media/ for every
	// edited memory image. Off by default: edits stay presentation-only.
	EditedCopies bool `json:"edited_copies"`
	// DefaultLayout is the layout given to new image blocks.
	DefaultLayout string `json:"default_layout"`
	// DefaultMode is the mode a memory opens in ("edit" or "preview").
	DefaultMode string `json:"default_mode"`
	// Autosave saves edits automatically when true; when false the editor
	// keeps a local draft and saves on Ctrl/Cmd+S or the Save button.
	Autosave bool `json:"autosave"`
}

// DefaultMemorySettings returns the settings a new account starts with.
func DefaultMemorySettings() MemorySettings {
	return MemorySettings{
		SlideshowInterval: DefaultSlideshowInterval,
		EditedCopies:      false,
		DefaultLayout:     "grid",
		DefaultMode:       "edit",
		Autosave:          true,
	}
}

// MemoryPatch changes some memory settings; nil fields are left unchanged.
type MemoryPatch struct {
	SlideshowInterval *int    `json:"slideshow_interval"`
	EditedCopies      *bool   `json:"edited_copies"`
	DefaultLayout     *string `json:"default_layout"`
	DefaultMode       *string `json:"default_mode"`
	Autosave          *bool   `json:"autosave"`
}

// ValidationError reports an out-of-range preference.
type ValidationError struct{ msg string }

func (e *ValidationError) Error() string { return e.msg }

// Store reads and writes preferences.
type Store struct {
	db *sql.DB
}

// NewStore wraps the server database.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

const memoryPrefix = "memories."

// Memory returns a user's memory settings with defaults filled in.
func (s *Store) Memory(ctx context.Context, userID string) (MemorySettings, error) {
	out := DefaultMemorySettings()
	rows, err := s.db.QueryContext(ctx,
		`SELECT key, value FROM user_settings WHERE user_id = ? AND key LIKE 'memories.%'`, userID)
	if err != nil {
		return out, fmt.Errorf("read user settings: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return out, err
		}
		// A malformed stored value falls back to the default rather than
		// failing the whole read.
		switch key {
		case memoryPrefix + "slideshow_interval":
			var v int
			if json.Unmarshal([]byte(value), &v) == nil && v >= MinSlideshowInterval && v <= MaxSlideshowInterval {
				out.SlideshowInterval = v
			}
		case memoryPrefix + "edited_copies":
			_ = json.Unmarshal([]byte(value), &out.EditedCopies)
		case memoryPrefix + "default_layout":
			var v string
			if json.Unmarshal([]byte(value), &v) == nil && memoryLayouts[v] {
				out.DefaultLayout = v
			}
		case memoryPrefix + "default_mode":
			var v string
			if json.Unmarshal([]byte(value), &v) == nil && editorModes[v] {
				out.DefaultMode = v
			}
		case memoryPrefix + "autosave":
			_ = json.Unmarshal([]byte(value), &out.Autosave)
		}
	}
	return out, rows.Err()
}

// UpdateMemory validates and applies a patch, returning the new settings.
func (s *Store) UpdateMemory(ctx context.Context, userID string, p MemoryPatch) (MemorySettings, error) {
	values := map[string]any{}
	if p.SlideshowInterval != nil {
		v := *p.SlideshowInterval
		if v < MinSlideshowInterval || v > MaxSlideshowInterval {
			return MemorySettings{}, &ValidationError{msg: fmt.Sprintf(
				"slideshow_interval must be between %d and %d seconds", MinSlideshowInterval, MaxSlideshowInterval)}
		}
		values["slideshow_interval"] = v
	}
	if p.EditedCopies != nil {
		values["edited_copies"] = *p.EditedCopies
	}
	if p.DefaultLayout != nil {
		if !memoryLayouts[*p.DefaultLayout] {
			return MemorySettings{}, &ValidationError{msg: fmt.Sprintf("unknown layout %q", *p.DefaultLayout)}
		}
		values["default_layout"] = *p.DefaultLayout
	}
	if p.DefaultMode != nil {
		if !editorModes[*p.DefaultMode] {
			return MemorySettings{}, &ValidationError{msg: "default_mode must be \"edit\" or \"preview\""}
		}
		values["default_mode"] = *p.DefaultMode
	}
	if p.Autosave != nil {
		values["autosave"] = *p.Autosave
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MemorySettings{}, err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for key, v := range values {
		raw, err := json.Marshal(v)
		if err != nil {
			return MemorySettings{}, err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO user_settings (user_id, key, value, updated_at) VALUES (?, ?, ?, ?)
			 ON CONFLICT(user_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			userID, memoryPrefix+key, string(raw), now); err != nil {
			return MemorySettings{}, fmt.Errorf("write user setting: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return MemorySettings{}, err
	}
	return s.Memory(ctx, userID)
}
