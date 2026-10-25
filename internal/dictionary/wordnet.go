package dictionary

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// wordNetSource is Open English WordNet in the classic WNDB file format
// (the format Princeton WordNet shipped in): sorted index files mapping a
// lemma to synset byte offsets in data files. The release is pinned by
// checksum, so a tampered or truncated download is refused.
var wordNetSource = Source{
	ID:   WordNetID,
	Kind: KindWordNet,
	Name: "WordNet",
	Description: "English definitions, examples and synonyms from Open English WordNet, " +
		"the maintained successor to Princeton WordNet.",
	Version:       "2024",
	License:       "CC BY 4.0",
	DownloadBytes: 11457906,
	URL:           "https://en-word.net/downloads/english-wordnet-2024.zip",
	Checksum:      "sha256:12ce54dd051b9420353f6c4ea74b34c736e23c8233d6d0bbd75407526f1fc732",
}

// wordNetPOS are WordNet's parts of speech: the suffix of their files and
// how a reader names them.
var wordNetPOS = []struct{ file, label string }{
	{"noun", "noun"},
	{"verb", "verb"},
	{"adj", "adjective"},
	{"adv", "adverb"},
}

// wordNetFiles are the files a lookup reads; the rest of the release
// (sense index, verb frames, counts) is left in the archive.
func wordNetFiles() map[string]bool {
	files := map[string]bool{}
	for _, p := range wordNetPOS {
		files["index."+p.file] = true
		files["data."+p.file] = true
		files[p.file+".exc"] = true
	}
	return files
}

const (
	maxSensesPerEntry = 8
	maxEntries        = 8
	maxSynonyms       = 6
)

// installWordNet unpacks the files lookups need from the release archive.
func installWordNet(ctx context.Context, job *Job, archive, dest string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("the WordNet download isn't a readable archive: %w", err)
	}
	defer func() { _ = zr.Close() }()

	wanted := wordNetFiles()
	var picked []*zip.File
	var total int64
	for _, f := range zr.File {
		// Only the bare names of known files are taken, which also keeps
		// a hostile archive from writing outside dest.
		name := path.Base(f.Name)
		if f.FileInfo().IsDir() || !wanted[name] {
			continue
		}
		picked = append(picked, f)
		total += int64(f.UncompressedSize64)
	}
	if len(picked) != len(wanted) {
		return fmt.Errorf("the WordNet download is missing files (found %d of %d)", len(picked), len(wanted))
	}

	job.setPhase(PhaseExtracting, total)
	for _, f := range picked {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := extractZipFile(f, filepath.Join(dest, path.Base(f.Name)), job); err != nil {
			return err
		}
	}
	return nil
}

func extractZipFile(f *zip.File, dest string, job *Job) error {
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("unpack %s: %w", f.Name, err)
	}
	defer func() { _ = rc.Close() }()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("unpack %s: %w", f.Name, err)
	}
	if _, err := io.Copy(out, &countingReader{r: rc, job: job}); err != nil {
		_ = out.Close()
		return fmt.Errorf("unpack %s: %w", f.Name, err)
	}
	return out.Close()
}

// countingReader adds the bytes read through it to a job's progress.
type countingReader struct {
	r   io.Reader
	job *Job
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.job.done.Add(int64(n))
	return n, err
}

type wordNetPart struct {
	label string
	index *sortedFile
	data  *sortedFile
	exc   *sortedFile
}

type wordNet struct {
	parts []wordNetPart
}

func openWordNet(dir string) (reader, error) {
	wn := &wordNet{}
	for _, p := range wordNetPOS {
		part := wordNetPart{label: p.label}
		var err error
		if part.index, err = openSortedFile(filepath.Join(dir, "index."+p.file), ' '); err != nil {
			_ = wn.Close()
			return nil, err
		}
		if part.data, err = openSortedFile(filepath.Join(dir, "data."+p.file), ' '); err != nil {
			_ = part.index.Close()
			_ = wn.Close()
			return nil, err
		}
		if part.exc, err = openSortedFile(filepath.Join(dir, p.file+".exc"), ' '); err != nil {
			_ = part.index.Close()
			_ = part.data.Close()
			_ = wn.Close()
			return nil, err
		}
		wn.parts = append(wn.parts, part)
	}
	return wn, nil
}

func (wn *wordNet) Close() error {
	for _, p := range wn.parts {
		_ = p.index.Close()
		_ = p.data.Close()
		_ = p.exc.Close()
	}
	return nil
}

func (wn *wordNet) lookup(word string) ([]Entry, error) {
	// WordNet's lemmas are lowercase with underscores for spaces.
	key := strings.ReplaceAll(strings.ToLower(word), " ", "_")
	var entries []Entry
	for _, part := range wn.parts {
		lemmas, err := part.lemmas(key)
		if err != nil {
			return nil, err
		}
		for _, lemma := range lemmas {
			senses, err := part.senses(lemma)
			if err != nil {
				return nil, err
			}
			if len(senses) == 0 {
				continue
			}
			entries = append(entries, Entry{
				Headword:     strings.ReplaceAll(lemma, "_", " "),
				PartOfSpeech: part.label,
				Senses:       senses,
			})
			if len(entries) == maxEntries {
				return entries, nil
			}
		}
	}
	return entries, nil
}

// morphRules are WordNet's "detachment" rules: an inflectional ending and
// what replaces it to reach the base form, per part of speech.
var morphRules = map[string][][2]string{
	"noun":      {{"s", ""}, {"ses", "s"}, {"xes", "x"}, {"zes", "z"}, {"ches", "ch"}, {"shes", "sh"}, {"men", "man"}, {"ies", "y"}},
	"verb":      {{"s", ""}, {"ies", "y"}, {"es", "e"}, {"es", ""}, {"ed", "e"}, {"ed", ""}, {"ing", "e"}, {"ing", ""}},
	"adjective": {{"er", ""}, {"est", ""}, {"er", "e"}, {"est", "e"}},
}

// lemmas are the base forms of word under this part of speech that the
// index knows: the word itself, its irregular forms from the exception
// list ("went" → "go"), or failing those, what the regular endings reduce
// it to ("running" → "run" is caught by doubling below).
func (p wordNetPart) lemmas(word string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	add := func(candidate string) error {
		if candidate == "" || seen[candidate] {
			return nil
		}
		seen[candidate] = true
		lines, err := p.index.lookup(candidate)
		if err != nil {
			return err
		}
		if len(lines) > 0 {
			out = append(out, candidate)
		}
		return nil
	}

	if err := add(word); err != nil {
		return nil, err
	}
	excLines, err := p.exc.lookup(word)
	if err != nil {
		return nil, err
	}
	for _, line := range excLines {
		for _, base := range strings.Fields(line)[1:] {
			if err := add(base); err != nil {
				return nil, err
			}
		}
	}
	if len(excLines) == 0 {
		for _, rule := range morphRules[p.label] {
			if !strings.HasSuffix(word, rule[0]) || len(word) <= len(rule[0])+1 {
				continue
			}
			stem := word[:len(word)-len(rule[0])] + rule[1]
			if err := add(stem); err != nil {
				return nil, err
			}
			// "running" → "runn" → "run": a doubled final consonant.
			if rule[1] == "" && len(stem) > 2 && stem[len(stem)-1] == stem[len(stem)-2] {
				if err := add(stem[:len(stem)-1]); err != nil {
					return nil, err
				}
			}
		}
	}
	return out, nil
}

// senses reads lemma's synsets, most common sense first (the index lists
// them in frequency order).
func (p wordNetPart) senses(lemma string) ([]Sense, error) {
	lines, err := p.index.lookup(lemma)
	if err != nil || len(lines) == 0 {
		return nil, err
	}
	offsets, err := indexOffsets(lines[0])
	if err != nil {
		return nil, err
	}
	var out []Sense
	for _, off := range offsets {
		line, _, err := p.data.lineAt(off)
		if err != nil {
			return nil, err
		}
		if s, ok := parseSynset(line, lemma); ok {
			out = append(out, s)
		}
		if len(out) == maxSensesPerEntry {
			break
		}
	}
	return out, nil
}

// indexOffsets reads an index line:
//
//	lemma pos synset_cnt p_cnt [ptr_symbol...] sense_cnt tagsense_cnt offset...
//
// whose last synset_cnt fields are the synsets' byte offsets.
func indexOffsets(line string) ([]int64, error) {
	f := strings.Fields(line)
	if len(f) < 4 {
		return nil, fmt.Errorf("malformed WordNet index line %q", line)
	}
	n, err := strconv.Atoi(f[2])
	if err != nil || n < 0 || n > len(f)-4 {
		return nil, fmt.Errorf("malformed WordNet index line %q", line)
	}
	out := make([]int64, 0, n)
	for _, s := range f[len(f)-n:] {
		off, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("malformed WordNet offset %q", s)
		}
		out = append(out, off)
	}
	return out, nil
}

var (
	quoted       = regexp.MustCompile(`"([^"]+)"`)
	syntaxMarker = regexp.MustCompile(`\([a-z]+\)$`)
)

// parseSynset reads a data line:
//
//	offset lex_filenum ss_type w_cnt word lex_id [word lex_id...] ... | gloss
//
// The gloss is a definition followed by quoted example sentences; the
// synset's other words are the lemma's synonyms in this sense.
func parseSynset(line, lemma string) (Sense, bool) {
	head, gloss, ok := strings.Cut(line, " | ")
	if !ok {
		return Sense{}, false
	}
	gloss = strings.TrimSpace(gloss)

	var s Sense
	if i := strings.IndexByte(gloss, '"'); i >= 0 {
		s.Definition = strings.TrimRight(gloss[:i], " ;:")
		for _, m := range quoted.FindAllStringSubmatch(gloss[i:], -1) {
			s.Examples = append(s.Examples, strings.TrimSpace(m[1]))
		}
	} else {
		s.Definition = gloss
	}
	if s.Definition == "" && len(s.Examples) == 0 {
		return Sense{}, false
	}

	f := strings.Fields(head)
	if len(f) >= 4 {
		if count, err := strconv.ParseInt(f[3], 16, 32); err == nil {
			for i := 0; i < int(count) && 4+2*i < len(f); i++ {
				w := syntaxMarker.ReplaceAllString(f[4+2*i], "")
				if strings.EqualFold(w, lemma) || len(s.Synonyms) == maxSynonyms {
					continue
				}
				s.Synonyms = append(s.Synonyms, strings.ReplaceAll(w, "_", " "))
			}
		}
	}
	return s, true
}
