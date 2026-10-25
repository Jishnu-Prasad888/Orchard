package dictionary

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
)

// sortedFile answers "which lines start with this key?" by binary search
// over a text file whose lines are sorted bytewise by their first field,
// reading only the few hundred bytes around each probe. WordNet's index and
// exception files ship in this shape, and FreeDict's index is rewritten
// into it at install time, so neither is ever loaded into memory — on a
// Raspberry Pi a lookup costs a handful of page-cache reads, not megabytes.
//
// A line's key is everything before sep. Lines whose key sorts before every
// real word (WordNet's license header starts with two spaces) are harmless:
// they simply compare low.
type sortedFile struct {
	f    *os.File
	size int64
	sep  byte
}

func openSortedFile(path string, sep byte) (*sortedFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &sortedFile{f: f, size: st.Size(), sep: sep}, nil
}

func (s *sortedFile) Close() error { return s.f.Close() }

// lookup returns every line whose key equals key, in file order, without
// the trailing newline.
func (s *sortedFile) lookup(key string) ([]string, error) {
	at, err := s.lowerBound(key)
	if err != nil {
		return nil, err
	}
	var out []string
	for at < s.size {
		line, next, err := s.lineAt(at)
		if err != nil {
			return nil, err
		}
		if s.key(line) != key {
			break
		}
		out = append(out, line)
		at = next
	}
	return out, nil
}

// lowerBound is the offset of the first line whose key is >= key (or the
// file size when there is none). lo is always a line start; hi is a line
// start or the end of the file.
func (s *sortedFile) lowerBound(key string) (int64, error) {
	lo, hi := int64(0), s.size
	for lo < hi {
		start, err := s.nextLineStart(lo + (hi-lo)/2)
		if err != nil {
			return 0, err
		}
		if start >= hi {
			// No line begins in the upper half: the line at lo is the only
			// one left to decide.
			start = lo
		}
		line, next, err := s.lineAt(start)
		if err != nil {
			return 0, err
		}
		if s.key(line) < key {
			lo = next
		} else {
			hi = start
		}
	}
	return lo, nil
}

func (s *sortedFile) key(line string) string {
	if i := strings.IndexByte(line, s.sep); i >= 0 {
		return line[:i]
	}
	return line
}

// nextLineStart is the first line start at or after pos.
func (s *sortedFile) nextLineStart(pos int64) (int64, error) {
	if pos <= 0 {
		return 0, nil
	}
	// A line starts at pos when the byte before it ends the previous line.
	buf := make([]byte, 256)
	at := pos - 1
	for at < s.size {
		n, err := s.f.ReadAt(buf, at)
		if i := bytes.IndexByte(buf[:n], '\n'); i >= 0 {
			return at + int64(i) + 1, nil
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return s.size, nil
			}
			return 0, err
		}
		at += int64(n)
	}
	return s.size, nil
}

// lineAt reads the line starting at start, returning it without its
// newline and the offset of the line after it.
func (s *sortedFile) lineAt(start int64) (string, int64, error) {
	var line []byte
	buf := make([]byte, 512)
	at := start
	for {
		n, err := s.f.ReadAt(buf, at)
		if i := bytes.IndexByte(buf[:n], '\n'); i >= 0 {
			line = append(line, buf[:i]...)
			return strings.TrimRight(string(line), "\r"), at + int64(i) + 1, nil
		}
		line = append(line, buf[:n]...)
		at += int64(n)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return strings.TrimRight(string(line), "\r"), s.size, nil
			}
			return "", 0, err
		}
	}
}
