package dictionary

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ulikunitz/xz"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestSortedFileLookup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "idx")
	content := "  1 license header\n  2 more header\nalpha x\nbeta 1\nbeta 2\ngamma y\nzeta z"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := openSortedFile(path, ' ')
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	cases := map[string][]string{
		"alpha": {"alpha x"},
		"beta":  {"beta 1", "beta 2"},
		"gamma": {"gamma y"},
		"zeta":  {"zeta z"}, // last line, no trailing newline
		"aaa":   nil,
		"delta": nil,
		"zzz":   nil,
		"bet":   nil, // a prefix of a key is not a match
	}
	for key, want := range cases {
		got, err := f.lookup(key)
		if err != nil {
			t.Fatalf("lookup(%q): %v", key, err)
		}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("lookup(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestSortedFileLookupManyLines(t *testing.T) {
	// Enough lines (with some long ones) that probes land mid-line and
	// across read-buffer boundaries.
	var keys []string
	for i := 0; i < 3000; i++ {
		keys = append(keys, fmt.Sprintf("w%05d", i*7))
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		b.WriteString(k + " " + strings.Repeat("x", i%900) + "\n")
	}
	path := filepath.Join(t.TempDir(), "idx")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := openSortedFile(path, ' ')
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	for i, k := range keys {
		got, err := f.lookup(k)
		if err != nil || len(got) != 1 || !strings.HasPrefix(got[0], k+" ") {
			t.Fatalf("lookup(%q) = %q, %v", k, got, err)
		}
		if missing := fmt.Sprintf("w%05d", i*7+1); i%100 == 0 {
			if got, _ := f.lookup(missing); len(got) != 0 {
				t.Fatalf("lookup(%q) = %q, want nothing", missing, got)
			}
		}
	}
}

func TestCleanWord(t *testing.T) {
	cases := map[string]string{
		"  house ":       "house",
		"“Hello,”":       "Hello",
		"dog's":          "dog",
		"dog’s":          "dog",
		"give\n up":      "give up",
		"(parenthesis).": "parenthesis",
		"well-known":     "well-known",
		"...":            "",
	}
	for in, want := range cases {
		if got := cleanWord(in); got != want {
			t.Errorf("cleanWord(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- WordNet fixture ---

type synset struct {
	pos   string
	words []string
	gloss string
}

// wordNetFixture writes a miniature WNDB database: data files with real
// byte offsets and index files pointing at them.
func wordNetFixture(t *testing.T, dir string) {
	t.Helper()
	synsets := []synset{
		{"noun", []string{"house"}, `a dwelling that serves as living quarters for one or more families; "he has a house on Cape Cod"`},
		{"noun", []string{"goose"}, "web-footed long-necked typically gregarious migratory aquatic birds"},
		{"noun", []string{"run", "tally"}, "a score in baseball made by a runner touching all four bases"},
		{"verb", []string{"run"}, `move fast by using one's feet; "Don't run--you'll be out of breath"; "The children ran to the store"`},
		{"verb", []string{"go", "move"}, "change location; move, travel, or proceed"},
		{"verb", []string{"give_up", "quit"}, "stop maintaining or insisting on"},
		{"adj", []string{"happy"}, `enjoying or showing or marked by joy or pleasure; "a happy smile"`},
	}
	header := "  1 This is a test license header.  \n"
	lemmaOffsets := map[string]map[string][]int64{}
	for _, p := range wordNetPOS {
		var data strings.Builder
		data.WriteString(header)
		lemmaOffsets[p.file] = map[string][]int64{}
		for _, s := range synsets {
			if s.pos != p.file {
				continue
			}
			off := int64(data.Len())
			fields := []string{fmt.Sprintf("%08d", off), "00", p.file[:1], fmt.Sprintf("%02x", len(s.words))}
			for _, w := range s.words {
				fields = append(fields, w, "0")
				lemmaOffsets[p.file][w] = append(lemmaOffsets[p.file][w], off)
			}
			fields = append(fields, "000")
			data.WriteString(strings.Join(fields, " ") + " | " + s.gloss + "  \n")
		}
		writeTestFile(t, filepath.Join(dir, "data."+p.file), data.String())

		var lemmas []string
		for l := range lemmaOffsets[p.file] {
			lemmas = append(lemmas, l)
		}
		sort.Strings(lemmas)
		var index strings.Builder
		index.WriteString(header)
		for _, l := range lemmas {
			offs := lemmaOffsets[p.file][l]
			line := fmt.Sprintf("%s %s %d 1 @ %d 0", l, p.file[:1], len(offs), len(offs))
			for _, o := range offs {
				line += fmt.Sprintf(" %08d", o)
			}
			index.WriteString(line + "  \n")
		}
		writeTestFile(t, filepath.Join(dir, "index."+p.file), index.String())
	}
	writeTestFile(t, filepath.Join(dir, "noun.exc"), "geese goose\n")
	writeTestFile(t, filepath.Join(dir, "verb.exc"), "ran run\nwent go\n")
	writeTestFile(t, filepath.Join(dir, "adj.exc"), "happier happy\n")
	writeTestFile(t, filepath.Join(dir, "adv.exc"), "")
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestWordNetLookup(t *testing.T) {
	dir := t.TempDir()
	wordNetFixture(t, dir)
	r, err := openWordNet(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	entries, err := r.lookup("house")
	if err != nil || len(entries) != 1 {
		t.Fatalf("lookup(house) = %+v, %v", entries, err)
	}
	s := entries[0].Senses[0]
	if entries[0].PartOfSpeech != "noun" ||
		s.Definition != "a dwelling that serves as living quarters for one or more families" ||
		len(s.Examples) != 1 || s.Examples[0] != "he has a house on Cape Cod" {
		t.Errorf("house = %+v", entries[0])
	}

	// Inflections reach their base form: irregular ones through the
	// exception lists, regular ones through the detachment rules.
	for word, want := range map[string]string{
		"geese":   "goose/noun",
		"went":    "go/verb",
		"ran":     "run/verb",
		"running": "run/verb",
		"runs":    "run/noun run/verb",
		"houses":  "house/noun",
		"happier": "happy/adjective",
		"Give Up": "give up/verb",
	} {
		entries, err := r.lookup(word)
		if err != nil {
			t.Fatalf("lookup(%q): %v", word, err)
		}
		var got []string
		for _, e := range entries {
			got = append(got, e.Headword+"/"+e.PartOfSpeech)
		}
		if strings.Join(got, " ") != want {
			t.Errorf("lookup(%q) = %v, want %s", word, got, want)
		}
	}

	entries, _ = r.lookup("run")
	if len(entries) != 2 || entries[0].Senses[0].Synonyms[0] != "tally" {
		t.Errorf("run synonyms = %+v", entries)
	}
	if v := entries[1].Senses[0]; len(v.Examples) != 2 || v.Definition != "move fast by using one's feet" {
		t.Errorf("run (verb) = %+v", v)
	}
	if entries, _ := r.lookup("zebra"); len(entries) != 0 {
		t.Errorf("lookup(zebra) = %+v, want nothing", entries)
	}
}

// --- FreeDict fixture ---

func dictdArchive(t *testing.T) []byte {
	t.Helper()
	entries := []struct{ head, body string }{
		{"house", "house /haus/ <n>\nmaison\n"},
		{"run", "run /rʌn/ <v>\n1. courir\n(intransitive) To move swiftly.\n2. fonctionner\n"},
		{"Run", "Run <n>\ncourse\n"},
		{"a lot", "a lot /əlɔt/\nbeaucoup\n"},
	}
	var dict bytes.Buffer
	var index []string
	enc := func(n int) string {
		if n == 0 {
			return "A"
		}
		s := ""
		for n > 0 {
			s = string(b64Alphabet[n%64]) + s
			n /= 64
		}
		return s
	}
	index = append(index, "00databaseshort\t"+enc(0)+"\t"+enc(0))
	for _, e := range entries {
		index = append(index, e.head+"\t"+enc(dict.Len())+"\t"+enc(len(e.body)))
		dict.WriteString(e.body)
	}
	var dz bytes.Buffer
	gz := gzip.NewWriter(&dz)
	_, _ = gz.Write(dict.Bytes())
	_ = gz.Close()

	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	for name, content := range map[string][]byte{
		"eng-fra/eng-fra.index":   []byte(strings.Join(index, "\n") + "\n"),
		"eng-fra/eng-fra.dict.dz": dz.Bytes(),
		"eng-fra/README":          []byte("readme"),
	} {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg})
		_, _ = tw.Write(content)
	}
	_ = tw.Close()

	var out bytes.Buffer
	xw, err := xz.NewWriter(&out)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = xw.Write(tarBuf.Bytes())
	_ = xw.Close()
	return out.Bytes()
}

func wordNetZip(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	wordNetFixture(t, dir)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name := range wordNetFiles() {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		w, _ := zw.Create("oewn2024/" + name)
		_, _ = w.Write(b)
	}
	w, _ := zw.Create("oewn2024/index.sense")
	_, _ = w.Write([]byte("unused"))
	_ = zw.Close()
	return buf.Bytes()
}

// testServer serves the WordNet zip, a FreeDict release and its catalog.
func testServer(t *testing.T, wn, fd []byte, fdChecksum string) (*httptest.Server, *Manager) {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/wordnet.zip", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(wn) })
	mux.HandleFunc("/eng-fra.tar.xz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(fd) })
	mux.HandleFunc("/catalog.json", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{
			map[string]any{"name": "eng-fra", "headwords": "4", "releases": []any{
				map[string]any{"platform": "src", "URL": "https://example.invalid/src.tar.xz", "checksum": "x"},
				map[string]any{"platform": "dictd", "URL": srv.URL + "/eng-fra.tar.xz",
					"checksum": fdChecksum, "size": fmt.Sprint(len(fd)), "version": "0.1.6"},
			}},
			map[string]any{"software": map[string]any{}},
		})
	})
	srv = httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)

	m, err := NewManager(t.TempDir(), quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	m.client = srv.Client()
	sum := sha256.Sum256(wn)
	m.wordNet.URL = srv.URL + "/wordnet.zip"
	m.wordNet.Checksum = "sha256:" + hex.EncodeToString(sum[:])
	m.wordNet.DownloadBytes = int64(len(wn))
	m.freeDictCatalogURL = srv.URL + "/catalog.json"
	return srv, m
}

func waitForJob(t *testing.T, m *Manager) JobStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if j := m.Job(); j != nil && j.FinishedAt != nil {
			return *j
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("install did not finish")
	return JobStatus{}
}

func TestInstallAndLookup(t *testing.T) {
	fd := dictdArchive(t)
	sum := sha512.Sum512(fd)
	_, m := testServer(t, wordNetZip(t), fd, hex.EncodeToString(sum[:]))
	ctx := context.Background()

	if m.Available() {
		t.Fatal("available before anything is installed")
	}
	sources, err := m.Catalog(ctx)
	if err != nil || len(sources) != 2 || sources[1].ID != "freedict-eng-fra" ||
		sources[1].Name != "FreeDict English → French" {
		t.Fatalf("catalog = %+v, %v", sources, err)
	}

	for _, id := range []string{WordNetID, "freedict-eng-fra"} {
		if _, err := m.Install(ctx, id); err != nil {
			t.Fatalf("install %s: %v", id, err)
		}
		if j := waitForJob(t, m); j.Phase != PhaseDone {
			t.Fatalf("install %s finished %s: %s", id, j.Phase, j.Error)
		}
	}
	if _, err := m.Install(ctx, WordNetID); !errors.Is(err, ErrInstalled) {
		t.Errorf("reinstall: %v, want ErrInstalled", err)
	}

	installed := m.Installed()
	if len(installed) != 2 || installed[0].ID != WordNetID || !installed[0].Enabled || installed[0].SizeBytes == 0 {
		t.Fatalf("installed = %+v", installed)
	}
	// Only what lookups need is kept.
	if _, err := os.Stat(filepath.Join(m.dir, WordNetID, "index.sense")); !os.IsNotExist(err) {
		t.Errorf("index.sense was unpacked: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.dir, "freedict-eng-fra", sourceIndex)); !os.IsNotExist(err) {
		t.Errorf("source index left behind: %v", err)
	}

	word, results, err := m.Lookup(" Running, ")
	if err != nil || word != "Running" || len(results) != 2 {
		t.Fatalf("lookup = %q %+v %v", word, results, err)
	}
	if results[0].DictionaryID != WordNetID || results[0].Entries[0].Headword != "run" {
		t.Errorf("wordnet result = %+v", results[0])
	}
	fr := results[1]
	if fr.DictionaryID != "freedict-eng-fra" || len(fr.Entries) != 2 {
		t.Fatalf("freedict result = %+v", fr)
	}
	run := fr.Entries[1]
	if run.Headword != "run" || run.PartOfSpeech != "verb" || run.Pronunciation != "/rʌn/" ||
		len(run.Senses) != 2 || run.Senses[0].Definition != "courir" ||
		run.Senses[0].Notes[0] != "(intransitive) To move swiftly." {
		t.Errorf("run = %+v", run)
	}
	if _, results, _ := m.Lookup("a  lot"); len(results) != 1 || results[0].Entries[0].Senses[0].Definition != "beaucoup" {
		t.Errorf("phrase lookup = %+v", results)
	}

	// Disabled dictionaries sit lookups out, and the setting survives a
	// restart.
	if _, err := m.SetEnabled(WordNetID, false); err != nil {
		t.Fatal(err)
	}
	if _, results, _ := m.Lookup("house"); len(results) != 1 || results[0].DictionaryID != "freedict-eng-fra" {
		t.Errorf("lookup with WordNet off = %+v", results)
	}
	reopened, err := NewManager(m.dir, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := reopened.Installed(); len(got) != 2 || got[0].Enabled {
		t.Errorf("after restart = %+v", got)
	}

	if err := m.Remove("freedict-eng-fra"); err != nil {
		t.Fatal(err)
	}
	if m.Available() {
		t.Error("available with every dictionary off or removed")
	}
	if _, err := os.Stat(filepath.Join(m.dir, "freedict-eng-fra")); !os.IsNotExist(err) {
		t.Errorf("removed dictionary still on disk: %v", err)
	}
	if _, _, err := m.Lookup("   "); !errors.Is(err, ErrBadWord) {
		t.Errorf("blank lookup: %v", err)
	}
}

func TestInstallRejectsBadChecksum(t *testing.T) {
	fd := dictdArchive(t)
	_, m := testServer(t, wordNetZip(t), fd, strings.Repeat("0", 128))
	if _, err := m.Install(context.Background(), "freedict-eng-fra"); err != nil {
		t.Fatal(err)
	}
	j := waitForJob(t, m)
	if j.Phase != PhaseFailed || !strings.Contains(j.Error, "checksum") {
		t.Fatalf("job = %+v", j)
	}
	if len(m.Installed()) != 0 {
		t.Error("damaged download was installed")
	}
	entries, _ := os.ReadDir(m.dir)
	if len(entries) != 0 {
		t.Errorf("staging left behind: %v", entries)
	}
}

func TestInstallCancel(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000000")
		_, _ = w.Write(make([]byte, 1000))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	m, err := NewManager(t.TempDir(), quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	m.client = srv.Client()
	m.wordNet.URL = srv.URL

	if _, err := m.Install(context.Background(), WordNetID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Install(context.Background(), WordNetID); !errors.Is(err, ErrBusy) {
		t.Errorf("second install: %v, want ErrBusy", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for m.Job().Done == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if j := m.Job(); j.Done != 1000 || j.Total != 1000000 || j.Phase != PhaseDownloading {
		t.Errorf("progress = %+v", j)
	}
	if err := m.Cancel(); err != nil {
		t.Fatal(err)
	}
	if j := waitForJob(t, m); j.Phase != PhaseCancelled {
		t.Errorf("job = %+v", j)
	}
	if err := m.Cancel(); !errors.Is(err, ErrNoJob) {
		t.Errorf("cancel with nothing running: %v", err)
	}
}

func TestParseFreeDictEntryWiktionaryLayout(t *testing.T) {
	body := "good //ɡʊd// /[ɡud]/ <adj>\n1. bueno 2.\n(of people or animals)\n 3.\nCompetent or talented.\n2. bueno, bon\nActing in the interest of what is beneficial.\n"
	e, ok := parseFreeDictEntry(body, "good")
	if !ok || e.PartOfSpeech != "adjective" || e.Pronunciation != "/ɡʊd/" || len(e.Senses) != 2 {
		t.Fatalf("entry = %+v", e)
	}
	if s := e.Senses[0]; s.Definition != "bueno" || len(s.Notes) != 2 || s.Notes[1] != "Competent or talented." {
		t.Errorf("first sense = %+v", s)
	}
}
