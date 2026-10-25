package dictionary

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ulikunitz/xz"
)

// freeDictCatalogURL lists every FreeDict dictionary and its downloads,
// each with a SHA-512 checksum.
const freeDictCatalogURL = "https://freedict.org/freedict-database.json"

// catalogTTL is how long a fetched catalog is reused: FreeDict publishes a
// few releases a year.
const catalogTTL = 12 * time.Hour

// maxCatalogBytes bounds the catalog download (it is about 600 KB).
const maxCatalogBytes = 8 << 20

// languageNames names FreeDict's ISO 639-3 language codes.
var languageNames = map[string]string{
	"afr": "Afrikaans", "ara": "Arabic", "ast": "Asturian", "bre": "Breton",
	"bul": "Bulgarian", "cat": "Catalan", "ces": "Czech", "ckb": "Central Kurdish",
	"cym": "Welsh", "dan": "Danish", "deu": "German", "ell": "Greek",
	"eng": "English", "epo": "Esperanto", "fin": "Finnish", "fra": "French",
	"gla": "Scottish Gaelic", "gle": "Irish", "hin": "Hindi", "hrv": "Croatian",
	"hun": "Hungarian", "ind": "Indonesian", "isl": "Icelandic", "ita": "Italian",
	"jpn": "Japanese", "kha": "Khasi", "kmr": "Northern Kurdish", "kur": "Kurdish",
	"lat": "Latin", "lit": "Lithuanian", "mkd": "Macedonian", "mlg": "Malagasy",
	"nld": "Dutch", "nno": "Norwegian Nynorsk", "nob": "Norwegian Bokmål",
	"nor": "Norwegian", "oci": "Occitan", "pol": "Polish", "por": "Portuguese",
	"rom": "Romani", "rus": "Russian", "san": "Sanskrit", "slk": "Slovak",
	"slv": "Slovenian", "spa": "Spanish", "srp": "Serbian", "swe": "Swedish",
	"swh": "Swahili", "tur": "Turkish", "wol": "Wolof", "zho": "Chinese",
}

func languageName(code string) string {
	if name, ok := languageNames[code]; ok {
		return name
	}
	return code
}

var pairPattern = regexp.MustCompile(`^[a-z]{3}-[a-z]{3}$`)

type freeDictRelease struct {
	URL      string `json:"URL"`
	Checksum string `json:"checksum"`
	Platform string `json:"platform"`
	Size     string `json:"size"`
	Version  string `json:"version"`
}

type freeDictEntry struct {
	Name      string            `json:"name"`
	Headwords string            `json:"headwords"`
	Edition   string            `json:"edition"`
	Releases  []freeDictRelease `json:"releases"`
}

// Catalog lists what can be installed: WordNet, then every FreeDict
// language pair. FreeDict's list comes from its website; when that can't be
// reached, WordNet is still offered and the error says why FreeDict isn't.
func (m *Manager) Catalog(ctx context.Context) ([]Source, error) {
	freeDict, err := m.freeDictCatalog(ctx)
	return append([]Source{m.wordNet}, freeDict...), err
}

func (m *Manager) source(ctx context.Context, id string) (Source, error) {
	if id == WordNetID {
		return m.wordNet, nil
	}
	if !strings.HasPrefix(id, freeDictPrefix) {
		return Source{}, ErrUnknownSource
	}
	sources, err := m.freeDictCatalog(ctx)
	if err != nil && len(sources) == 0 {
		return Source{}, fmt.Errorf("couldn't load FreeDict's list of dictionaries: %w", err)
	}
	for _, s := range sources {
		if s.ID == id {
			return s, nil
		}
	}
	return Source{}, ErrUnknownSource
}

// freeDictCatalog returns FreeDict's dictionaries, fetched at most every
// catalogTTL. A stale list is returned alongside a fetch error.
func (m *Manager) freeDictCatalog(ctx context.Context) ([]Source, error) {
	m.catalogMu.Lock()
	defer m.catalogMu.Unlock()
	if m.catalog != nil && time.Since(m.catalogFetched) < catalogTTL {
		return m.catalog, nil
	}
	sources, err := m.fetchFreeDictCatalog(ctx)
	if err != nil {
		return m.catalog, err
	}
	m.catalog = sources
	m.catalogFetched = time.Now()
	return sources, nil
}

func (m *Manager) fetchFreeDictCatalog(ctx context.Context) ([]Source, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.freeDictCatalogURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("couldn't reach freedict.org: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("freedict.org answered %s", resp.Status)
	}
	// The catalog is a JSON array of dictionaries, with one trailing
	// object describing FreeDict's tools (no "name"), skipped below.
	var raw []freeDictEntry
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxCatalogBytes)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("couldn't read FreeDict's list of dictionaries: %w", err)
	}
	return parseFreeDictCatalog(raw), nil
}

func parseFreeDictCatalog(raw []freeDictEntry) []Source {
	var out []Source
	for _, e := range raw {
		if !pairPattern.MatchString(e.Name) {
			continue
		}
		for _, r := range e.Releases {
			if r.Platform != "dictd" || !strings.HasPrefix(r.URL, "https://") || r.Checksum == "" {
				continue
			}
			from, to, _ := strings.Cut(e.Name, "-")
			size, _ := strconv.ParseInt(r.Size, 10, 64)
			headwords, _ := strconv.Atoi(e.Headwords)
			name := fmt.Sprintf("FreeDict %s → %s", languageName(from), languageName(to))
			out = append(out, Source{
				ID:   freeDictPrefix + e.Name,
				Kind: KindFreeDict,
				Name: name,
				Description: fmt.Sprintf("Translations from %s into %s.",
					languageName(from), languageName(to)),
				Version:       r.Version,
				License:       "GPL / free (see dictionary)",
				DownloadBytes: size,
				Headwords:     headwords,
				URL:           r.URL,
				Checksum:      "sha512:" + r.Checksum,
			})
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Files of an installed FreeDict dictionary: its definitions, decompressed
// so any one can be read by offset, and an index sorted for binary search.
const (
	freeDictData  = "entries.dict"
	freeDictIndex = "words.idx"
	sourceIndex   = "source.index"
)

// installFreeDict unpacks a dictd release (a .tar.xz holding <pair>.index
// and <pair>.dict.dz) and rebuilds its index for lookups.
func installFreeDict(ctx context.Context, job *Job, archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return err
	}

	job.setPhase(PhaseExtracting, st.Size())
	xr, err := xz.NewReader(bufio.NewReader(&countingReader{r: f, job: job}))
	if err != nil {
		return fmt.Errorf("the FreeDict download isn't a readable archive: %w", err)
	}
	tr := tar.NewReader(xr)
	var haveIndex, haveData bool
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("the FreeDict download is damaged: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		// Only the bare names of the two files used are written, which also
		// keeps a hostile archive from writing outside dest.
		name := path.Base(hdr.Name)
		switch {
		case strings.HasSuffix(name, ".index"):
			err = writeFile(filepath.Join(dest, sourceIndex), tr)
			haveIndex = err == nil
		case strings.HasSuffix(name, ".dict.dz"):
			var gz *gzip.Reader
			if gz, err = gzip.NewReader(tr); err == nil {
				err = writeFile(filepath.Join(dest, freeDictData), gz)
			}
			haveData = err == nil
		case strings.HasSuffix(name, ".dict"):
			err = writeFile(filepath.Join(dest, freeDictData), tr)
			haveData = err == nil
		}
		if err != nil {
			return fmt.Errorf("unpack %s: %w", name, err)
		}
	}
	if !haveIndex || !haveData {
		return errors.New("the FreeDict download doesn't contain a dictionary")
	}
	return buildFreeDictIndex(ctx, job, dest)
}

func writeFile(dest string, r io.Reader) error {
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// buildFreeDictIndex rewrites dictd's index — sorted by its own collation,
// offsets in base64 — as lines of
//
//	key \t headword \t offset \t length
//
// sorted bytewise by key (the lowercased headword), which sortedFile can
// binary-search. This holds the index in memory once, briefly; the largest
// FreeDict index (English–German) is about 12 MB.
func buildFreeDictIndex(ctx context.Context, job *Job, dir string) error {
	src := filepath.Join(dir, sourceIndex)
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	job.setPhase(PhaseIndexing, st.Size())

	var lines []string
	sc := bufio.NewScanner(&countingReader{r: f, job: job})
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), "\t")
		if len(fields) < 3 || strings.HasPrefix(fields[0], "00database") {
			continue
		}
		off, err1 := decodeB64(fields[1])
		size, err2 := decodeB64(fields[2])
		key := normalizeKey(fields[0])
		if err1 != nil || err2 != nil || key == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s\t%s\t%d\t%d", key, fields[0], off, size))
	}
	_ = f.Close()
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read FreeDict index: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Sorting whole lines sorts by key: the tab ending a key sorts below
	// every character a key can contain, so "ab\t…" precedes "abc\t…".
	// Entries sharing a key fall in headword, then offset (dictionary) order.
	sort.Strings(lines)

	out, err := os.OpenFile(filepath.Join(dir, freeDictIndex), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(out)
	for _, line := range lines {
		_, _ = w.WriteString(line)
		_ = w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Remove(src)
}

const b64Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// decodeB64 reads dictd's base64 numbers (big-endian digits, no padding).
func decodeB64(s string) (int64, error) {
	if s == "" || len(s) > 10 {
		return 0, fmt.Errorf("bad dictd number %q", s)
	}
	var n int64
	for i := 0; i < len(s); i++ {
		d := strings.IndexByte(b64Alphabet, s[i])
		if d < 0 {
			return 0, fmt.Errorf("bad dictd number %q", s)
		}
		n = n*64 + int64(d)
	}
	return n, nil
}

// normalizeKey is how headwords are matched: case-insensitively, with runs
// of whitespace treated as one space.
func normalizeKey(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// maxEntryBytes skips an implausibly large entry rather than reading it.
const maxEntryBytes = 256 << 10

type freeDict struct {
	index *sortedFile
	data  *os.File
	// english is set for dictionaries translating from English, whose
	// lookups fall back to stripping inflections ("houses" → "house").
	english bool
}

func openFreeDict(dir string) (reader, error) {
	idx, err := openSortedFile(filepath.Join(dir, freeDictIndex), '\t')
	if err != nil {
		return nil, err
	}
	data, err := os.Open(filepath.Join(dir, freeDictData))
	if err != nil {
		_ = idx.Close()
		return nil, err
	}
	return &freeDict{
		index:   idx,
		data:    data,
		english: strings.HasPrefix(filepath.Base(dir), freeDictPrefix+"eng-"),
	}, nil
}

func (d *freeDict) Close() error {
	_ = d.index.Close()
	return d.data.Close()
}

func (d *freeDict) lookup(word string) ([]Entry, error) {
	key := normalizeKey(word)
	candidates := []string{key}
	if d.english {
		candidates = append(candidates, englishStems(key)...)
	}
	for _, c := range candidates {
		entries, err := d.lookupKey(c)
		if err != nil || len(entries) > 0 {
			return entries, err
		}
	}
	return nil, nil
}

func (d *freeDict) lookupKey(key string) ([]Entry, error) {
	lines, err := d.index.lookup(key)
	if err != nil {
		return nil, err
	}
	var out []Entry
	seen := map[string]bool{}
	for _, line := range lines {
		f := strings.Split(line, "\t")
		if len(f) != 4 {
			continue
		}
		off, err1 := strconv.ParseInt(f[2], 10, 64)
		size, err2 := strconv.ParseInt(f[3], 10, 64)
		if err1 != nil || err2 != nil || size <= 0 || size > maxEntryBytes {
			continue
		}
		buf := make([]byte, size)
		if _, err := d.data.ReadAt(buf, off); err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		body := string(buf)
		if seen[body] {
			continue
		}
		seen[body] = true
		if e, ok := parseFreeDictEntry(body, f[1]); ok {
			out = append(out, e)
		}
		if len(out) == maxEntries {
			break
		}
	}
	return out, nil
}

// englishStems are the base forms an inflected English word might have,
// tried in order when the word itself isn't a headword.
func englishStems(w string) []string {
	var out []string
	add := func(suffix, repl string) {
		if strings.HasSuffix(w, suffix) && len(w) > len(suffix)+1 {
			out = append(out, w[:len(w)-len(suffix)]+repl)
		}
	}
	add("ies", "y")
	add("es", "")
	add("s", "")
	add("ied", "y")
	add("ed", "e")
	add("ed", "")
	add("ing", "e")
	add("ing", "")
	if strings.HasSuffix(w, "ing") || strings.HasSuffix(w, "ed") {
		// "running" → "run", "stopped" → "stop".
		stem := strings.TrimSuffix(strings.TrimSuffix(w, "ing"), "ed")
		if n := len(stem); n > 2 && stem[n-1] == stem[n-2] {
			out = append(out, stem[:n-1])
		}
	}
	return out
}

var (
	posTag        = regexp.MustCompile(`<([^<>]+)>\s*$`)
	pronunciation = regexp.MustCompile(`/+([^/]+)/+`)
	numbered      = regexp.MustCompile(`^(\d+)\.\s+(.*)$`)
	subMarker     = regexp.MustCompile(`^\d+\.$`)
	trailingNum   = regexp.MustCompile(`\s+\d+\.$`)
)

var posNames = map[string]string{
	"n": "noun", "v": "verb", "vt": "verb", "vi": "verb", "adj": "adjective",
	"adv": "adverb", "prep": "preposition", "pron": "pronoun", "conj": "conjunction",
	"interj": "interjection", "num": "numeral", "art": "article",
}

const maxNotes = 6

// parseFreeDictEntry reads a dictd entry: a headword line (headword,
// pronunciations between slashes, a part of speech in angle brackets), then
// translations, numbered when there are several, each optionally followed
// by explanatory lines. FreeDict's dictionaries come from many sources and
// vary in layout; anything unrecognized is kept as plain text.
func parseFreeDictEntry(body, headword string) (Entry, bool) {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) == 0 {
		return Entry{}, false
	}
	e := Entry{Headword: headword}
	first := lines[0]
	if m := posTag.FindStringSubmatch(first); m != nil {
		pos := strings.TrimSpace(m[1])
		if name, ok := posNames[strings.ToLower(strings.TrimSuffix(pos, "."))]; ok {
			pos = name
		}
		e.PartOfSpeech = pos
	}
	if m := pronunciation.FindStringSubmatch(first); m != nil {
		e.Pronunciation = "/" + strings.Trim(m[1], "[]") + "/"
	}

	var cur *Sense
	newNote := true
	for _, raw := range lines[1:] {
		t := strings.TrimSpace(raw)
		if subMarker.MatchString(t) {
			// " 3." on its own line: the next lines are another sub-sense.
			newNote = true
			continue
		}
		t = strings.TrimSpace(trailingNum.ReplaceAllString(t, ""))
		if t == "" {
			continue
		}
		if m := numbered.FindStringSubmatch(t); m != nil {
			e.Senses = append(e.Senses, Sense{Definition: m[2]})
			cur = &e.Senses[len(e.Senses)-1]
			newNote = true
			continue
		}
		if cur == nil {
			e.Senses = append(e.Senses, Sense{Definition: t})
			cur = &e.Senses[len(e.Senses)-1]
			newNote = true
			continue
		}
		if newNote || len(cur.Notes) == 0 {
			if len(cur.Notes) < maxNotes {
				cur.Notes = append(cur.Notes, t)
			}
			newNote = false
		} else {
			cur.Notes[len(cur.Notes)-1] += " " + t
		}
	}
	if len(e.Senses) > 12 {
		e.Senses = e.Senses[:12]
	}
	return e, len(e.Senses) > 0
}
