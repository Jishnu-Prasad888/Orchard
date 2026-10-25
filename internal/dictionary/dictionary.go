// Package dictionary installs offline dictionaries onto the server and looks
// words up in them, for the reader's "Search in dictionary".
//
// Two sources are offered: Open English WordNet (English definitions, the
// maintained continuation of Princeton WordNet) and FreeDict (bilingual
// translation dictionaries, one per language pair). Each is downloaded once
// into <data>/dictionaries/<id>/ and read from disk on every lookup by
// binary search over sorted index files, so nothing is held in memory
// between lookups and nothing leaves the server while reading.
//
// Installing runs as one background job at a time (a Raspberry Pi has
// neither the bandwidth nor the I/O for two): the download streams to the
// data disk while its progress is published for the settings page to poll,
// and the finished dictionary is moved into place with a rename, so a
// half-installed one is never visible to lookups.
package dictionary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Kinds of dictionary.
const (
	KindWordNet  = "wordnet"
	KindFreeDict = "freedict"
)

// WordNetID is the id the WordNet dictionary is installed under. FreeDict
// dictionaries are "freedict-<pair>", e.g. "freedict-eng-deu".
const WordNetID = "wordnet"

const freeDictPrefix = "freedict-"

// Errors callers map to responses.
var (
	ErrNotFound      = errors.New("dictionary not found")
	ErrBusy          = errors.New("another dictionary is already being installed")
	ErrInstalled     = errors.New("dictionary is already installed")
	ErrUnknownSource = errors.New("unknown dictionary")
	ErrNoJob         = errors.New("no dictionary is being installed")
	ErrBadWord       = errors.New("enter a word to look up")
)

// maxLookupRunes bounds what a lookup accepts: a word or a short phrase
// ("give up", "a priori"), never a paragraph someone selected by accident.
const maxLookupRunes = 64

// Installed describes a dictionary on disk. It is also the manifest.json
// written beside the dictionary's files.
type Installed struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Version     string    `json:"version"`
	License     string    `json:"license"`
	SizeBytes   int64     `json:"size_bytes"`
	InstalledAt time.Time `json:"installed_at"`
	Enabled     bool      `json:"enabled"`
}

// Source is a dictionary that can be installed.
type Source struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	License     string `json:"license"`
	// DownloadBytes is the size of the download, when known.
	DownloadBytes int64 `json:"download_bytes"`
	// Headwords is how many words the dictionary covers, when known.
	Headwords int    `json:"headwords,omitempty"`
	URL       string `json:"-"`
	// Checksum verifies the download: "sha256:<hex>" or "sha512:<hex>".
	Checksum string `json:"-"`
}

// Sense is one meaning of a word.
type Sense struct {
	Definition string   `json:"definition"`
	Examples   []string `json:"examples,omitempty"`
	Synonyms   []string `json:"synonyms,omitempty"`
	// Notes explain a translation (FreeDict): its usage, register, or an
	// English gloss.
	Notes []string `json:"notes,omitempty"`
}

// Entry is a headword's senses under one part of speech.
type Entry struct {
	Headword      string  `json:"headword"`
	PartOfSpeech  string  `json:"part_of_speech,omitempty"`
	Pronunciation string  `json:"pronunciation,omitempty"`
	Senses        []Sense `json:"senses"`
}

// Result is what one dictionary knows about a word.
type Result struct {
	DictionaryID   string  `json:"dictionary_id"`
	DictionaryName string  `json:"dictionary_name"`
	Entries        []Entry `json:"entries"`
}

// reader is an installed dictionary opened for lookups.
type reader interface {
	lookup(word string) ([]Entry, error)
	Close() error
}

type installedDict struct {
	info   Installed
	reader reader // nil until first used
}

// Manager owns the dictionaries directory.
type Manager struct {
	dir    string
	logger *slog.Logger
	client *http.Client

	// wordNet is the WordNet download; a field so tests can point it at a
	// local server.
	wordNet Source
	// freeDictCatalogURL lists FreeDict's releases.
	freeDictCatalogURL string

	mu        sync.RWMutex
	installed map[string]*installedDict
	job       *Job
	cancel    context.CancelFunc

	catalogMu      sync.Mutex
	catalog        []Source
	catalogFetched time.Time
}

// NewManager loads the dictionaries already installed under dir (created
// if missing). Leftovers of an install interrupted by a crash or restart
// are cleared away.
func NewManager(dir string, logger *slog.Logger) (*Manager, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create dictionaries directory: %w", err)
	}
	m := &Manager{
		dir:    dir,
		logger: logger,
		// No overall timeout: a 20 MB download over a slow link can take
		// minutes. Stalls are caught per read instead (see download).
		client:             &http.Client{Transport: newTransport()},
		wordNet:            wordNetSource,
		freeDictCatalogURL: freeDictCatalogURL,
		installed:          map[string]*installedDict{},
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read dictionaries directory: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if strings.HasPrefix(e.Name(), ".") {
			_ = os.RemoveAll(path)
			continue
		}
		info, err := readManifest(path)
		if err != nil {
			logger.Warn("skip unreadable dictionary", "dir", path, "error", err)
			continue
		}
		m.installed[info.ID] = &installedDict{info: info}
	}
	return m, nil
}

// Close releases open dictionary files and stops a running install.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}
	for _, d := range m.installed {
		if d.reader != nil {
			_ = d.reader.Close()
			d.reader = nil
		}
	}
}

// Installed lists installed dictionaries, WordNet first, then by name.
func (m *Manager) Installed() []Installed {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Installed, 0, len(m.installed))
	for _, d := range m.installed {
		out = append(out, d.info)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Kind == KindWordNet) != (out[j].Kind == KindWordNet) {
			return out[i].Kind == KindWordNet
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Available reports whether any installed dictionary is enabled — whether
// the reader should offer "Search in dictionary" at all.
func (m *Manager) Available() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, d := range m.installed {
		if d.info.Enabled {
			return true
		}
	}
	return false
}

// SetEnabled turns an installed dictionary on or off for lookups.
func (m *Manager) SetEnabled(id string, enabled bool) (Installed, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.installed[id]
	if !ok {
		return Installed{}, ErrNotFound
	}
	next := d.info
	next.Enabled = enabled
	if err := writeManifest(filepath.Join(m.dir, id), next); err != nil {
		return Installed{}, err
	}
	d.info = next
	if !enabled && d.reader != nil {
		_ = d.reader.Close()
		d.reader = nil
	}
	return next, nil
}

// Remove uninstalls a dictionary, deleting its files.
func (m *Manager) Remove(id string) error {
	m.mu.Lock()
	d, ok := m.installed[id]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	if d.reader != nil {
		_ = d.reader.Close()
	}
	delete(m.installed, id)
	m.mu.Unlock()

	// Move it aside first so a crash halfway through deleting never leaves
	// a dictionary that looks installed but is missing files.
	path := filepath.Join(m.dir, id)
	trash := filepath.Join(m.dir, ".remove-"+id)
	_ = os.RemoveAll(trash)
	if err := os.Rename(path, trash); err != nil {
		return fmt.Errorf("remove dictionary: %w", err)
	}
	return os.RemoveAll(trash)
}

// Lookup looks word up in every enabled dictionary. A dictionary that
// knows nothing about the word is left out of the results.
func (m *Manager) Lookup(word string) (string, []Result, error) {
	word = cleanWord(word)
	if word == "" || len([]rune(word)) > maxLookupRunes {
		return word, nil, ErrBadWord
	}

	m.mu.Lock()
	type target struct {
		info   Installed
		reader reader
	}
	var targets []target
	for id, d := range m.installed {
		if !d.info.Enabled {
			continue
		}
		if d.reader == nil {
			r, err := openReader(filepath.Join(m.dir, id), d.info.Kind)
			if err != nil {
				m.logger.Error("open dictionary", "id", id, "error", err)
				continue
			}
			d.reader = r
		}
		targets = append(targets, target{d.info, d.reader})
	}
	m.mu.Unlock()
	sort.Slice(targets, func(i, j int) bool {
		if (targets[i].info.Kind == KindWordNet) != (targets[j].info.Kind == KindWordNet) {
			return targets[i].info.Kind == KindWordNet
		}
		return targets[i].info.Name < targets[j].info.Name
	})

	results := []Result{}
	for _, t := range targets {
		entries, err := t.reader.lookup(word)
		if err != nil {
			// A reader closed by a concurrent disable/remove, or a disk
			// error: this dictionary sits the lookup out.
			m.logger.Warn("dictionary lookup", "id", t.info.ID, "error", err)
			continue
		}
		if len(entries) > 0 {
			results = append(results, Result{
				DictionaryID:   t.info.ID,
				DictionaryName: t.info.Name,
				Entries:        entries,
			})
		}
	}
	return word, results, nil
}

func openReader(dir, kind string) (reader, error) {
	switch kind {
	case KindWordNet:
		return openWordNet(dir)
	case KindFreeDict:
		return openFreeDict(dir)
	default:
		return nil, fmt.Errorf("unknown dictionary kind %q", kind)
	}
}

// cleanWord trims what a reader's selection drags along with a word:
// surrounding whitespace, quotes and punctuation, and a possessive 's.
func cleanWord(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.TrimFunc(s, func(r rune) bool {
		return unicode.IsPunct(r) || unicode.IsSymbol(r) || unicode.IsSpace(r)
	})
	for _, suffix := range []string{"'s", "’s"} {
		if len(s) > len(suffix)+1 && strings.HasSuffix(strings.ToLower(s), suffix) {
			s = s[:len(s)-len(suffix)]
		}
	}
	return s
}

const manifestName = "manifest.json"

func readManifest(dir string) (Installed, error) {
	var info Installed
	b, err := os.ReadFile(filepath.Join(dir, manifestName))
	if err != nil {
		return info, err
	}
	if err := json.Unmarshal(b, &info); err != nil {
		return info, err
	}
	if info.ID == "" || info.ID != filepath.Base(dir) {
		return info, fmt.Errorf("manifest id %q does not match its directory", info.ID)
	}
	return info, nil
}

// writeManifest replaces dir's manifest atomically: written beside it,
// then renamed over it.
func writeManifest(dir string, info Installed) error {
	b, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, manifestName+".tmp")
	if err := os.WriteFile(tmp, b, 0o640); err != nil {
		return fmt.Errorf("write dictionary manifest: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, manifestName)); err != nil {
		return fmt.Errorf("write dictionary manifest: %w", err)
	}
	return nil
}

// dirSize is the total size of the regular files in dir.
func dirSize(dir string) int64 {
	var total int64
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
	}
	return total
}
