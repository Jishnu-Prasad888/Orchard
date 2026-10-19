package bookmeta

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
)

// The PDF renderer is PDFium compiled to WebAssembly. Turning that into
// native code takes ~2.3 s and ~300 MB of memory on a desktop, and much
// longer on a Raspberry Pi, every time the server starts. wazero can keep the
// compiled code on disk instead (~21 MB), which makes later starts take ~70 ms
// and ~80 MB.
//
// That cache is native machine code the process will execute, and it lives
// on an SD card that loses power without warning, so it is never trusted
// blindly:
//
//   - It is sealed only after a run proved it good: once cached code has
//     executed and the process is still alive, every entry's size and SHA-256
//     go into a manifest, written atomically and fsynced.
//   - Before each use every entry is checked against the manifest. Anything
//     unexpected — a missing or unreadable manifest, a changed or truncated
//     entry, an entry the manifest doesn't list — wipes it, and PDFium is
//     compiled afresh. (wazero's own check is a CRC over the code alone, and
//     on a bad entry it fails the compile outright rather than recompiling.)
//   - If wazero still rejects a verified cache, it is wiped and the renderer
//     starts uncached, so covers never stop working because of the cache.
//   - A marker is written before cached code first runs and removed once it
//     has. Finding it on the next start means the last process died while
//     running cached code. After maxLoadStrikes such deaths without a clean
//     cached run in between, the cache is disabled for this build, so a bad
//     cache can never turn into a crash loop.
//   - Each wazero version, Go version and platform gets its own folder; any
//     other folders (from before an upgrade) are deleted.
//
// The cache belongs in the server's data folder, never on a library drive: a
// library may be shared or removable, and whoever can write this folder can
// run code inside Orchard.

const (
	cacheManifest  = "manifest.json"
	cacheMarker    = "loading"
	cacheStrikes   = "strikes"
	cacheDisabled  = "disabled"
	cacheDirPrefix = "pdfium-"
	// maxLoadStrikes is how many starts may die running cached code (with no
	// clean cached run between them) before the cache is turned off.
	maxLoadStrikes = 2
	// Marker contents: whether the run that wrote it was loading cached code.
	markerWarm = "warm"
	markerCold = "cold"
)

// wazero keeps entries in a "wazero-<version>-<arch>-<os>" subfolder of the
// folder it is given, each named by a hex SHA-256 key. Manifest keys are
// those relative paths.
var (
	cacheEntryName  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	cacheEntryDir   = regexp.MustCompile(`^wazero-[A-Za-z0-9._-]+$`)
	cacheEntryPath  = regexp.MustCompile(`^wazero-[A-Za-z0-9._-]+/[0-9a-f]{64}$`)
	cacheBookkeeper = map[string]bool{cacheManifest: true, cacheMarker: true, cacheStrikes: true, cacheDisabled: true}
)

var (
	rendererCacheBase   string
	rendererCacheLogger = slog.Default()
)

// SetPDFRendererCache sets the folder (in the server's data directory) where
// the compiled PDF renderer is kept between runs. Call it before any PDF is
// processed. Without it the renderer is compiled on every start.
func SetPDFRendererCache(dir string, logger *slog.Logger) {
	rendererCacheBase = dir
	if logger != nil {
		rendererCacheLogger = logger
	}
}

type cacheManifestFile struct {
	Entries map[string]cacheEntry `json:"entries"`
}

type cacheEntry struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// rendererCache is one prepared, verified cache folder.
type rendererCache struct {
	dir     string
	warm    bool // it held verified compiled code when this run started
	logger  *slog.Logger
	confirm sync.Once
}

// prepareRendererCache readies the cache under base for this run, or returns
// nil if the renderer should run uncached. It never fails the caller: any
// problem just means compiling PDFium afresh.
func prepareRendererCache(base string, logger *slog.Logger) *rendererCache {
	if base == "" {
		return nil
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		logger.Warn("pdf renderer cache unavailable", "dir", base, "error", err)
		return nil
	}
	name := cacheDirPrefix + cacheBuildID()
	dir := filepath.Join(base, name)
	removeOtherBuilds(base, name, logger)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		logger.Warn("pdf renderer cache unavailable", "dir", dir, "error", err)
		return nil
	}
	if _, err := os.Stat(filepath.Join(dir, cacheDisabled)); err == nil {
		return nil
	}

	strikes := readStrikes(dir)
	if marker, err := os.ReadFile(filepath.Join(dir, cacheMarker)); err == nil {
		// The last run never confirmed. If it was running cached code, that
		// may be what killed it; a cold run dying (shutdown mid-compile, a
		// power cut) says nothing about the cache.
		if strings.TrimSpace(string(marker)) == markerWarm {
			strikes++
			logger.Warn("the last run stopped while using the pdf renderer cache; rebuilding it",
				"dir", dir, "strikes", strikes)
		}
		wipeCache(dir)
		if strikes >= maxLoadStrikes {
			logger.Warn("pdf renderer cache disabled after repeated failures; PDFium will be compiled on each start",
				"dir", dir)
			_ = writeFileSync(filepath.Join(dir, cacheDisabled), []byte("disabled after repeated load failures\n"))
			_ = os.Remove(filepath.Join(dir, cacheStrikes))
			_ = os.Remove(filepath.Join(dir, cacheMarker))
			syncDir(dir)
			return nil
		}
		if err := writeFileSync(filepath.Join(dir, cacheStrikes), []byte(strconv.Itoa(strikes))); err != nil {
			logger.Warn("record pdf renderer cache failure", "error", err)
		}
	}

	warm, err := verifyCache(dir)
	if err != nil {
		logger.Warn("pdf renderer cache failed verification; rebuilding it", "dir", dir, "reason", err)
		wipeCache(dir)
		warm = false
	}

	state := markerCold
	if warm {
		state = markerWarm
	}
	if err := writeFileSync(filepath.Join(dir, cacheMarker), []byte(state)); err != nil {
		// Without the marker a crash could not be detected; don't load code.
		logger.Warn("pdf renderer cache unavailable", "dir", dir, "error", err)
		wipeCache(dir)
		return nil
	}
	syncDir(dir)
	return &rendererCache{dir: dir, warm: warm, logger: logger}
}

// confirmed records that this run executed the renderer and survived: the
// cache is sealed with a fresh manifest, and (for a run that loaded cached
// code) its failure count is cleared. Later calls do nothing.
func (c *rendererCache) confirmed() {
	if c == nil {
		return
	}
	c.confirm.Do(func() {
		manifest, err := hashEntries(c.dir)
		if err == nil {
			err = writeJSONSync(filepath.Join(c.dir, cacheManifest), manifest)
		}
		if err != nil {
			// Unsealed, the next start will discard it and compile again.
			c.logger.Warn("seal pdf renderer cache", "dir", c.dir, "error", err)
			_ = os.Remove(filepath.Join(c.dir, cacheManifest))
		}
		if c.warm {
			_ = os.Remove(filepath.Join(c.dir, cacheStrikes))
		}
		_ = os.Remove(filepath.Join(c.dir, cacheMarker))
		syncDir(c.dir)
	})
}

// discard empties a cache wazero refused, so this run continues uncached
// and the next one compiles afresh. It is not counted as a crash.
func (c *rendererCache) discard() {
	if c == nil {
		return
	}
	wipeCache(c.dir)
	_ = os.Remove(filepath.Join(c.dir, cacheMarker))
	syncDir(c.dir)
}

// verifyCache checks every entry against the manifest. warm reports that it
// holds verified compiled code. Unlisted files (such as wazero's .tmp files
// from an interrupted write) are removed; anything else wrong is an error.
func verifyCache(dir string) (warm bool, err error) {
	found, err := scanCache(dir)
	if err != nil {
		return false, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, cacheManifest))
	if errors.Is(err, os.ErrNotExist) {
		if len(found) > 0 {
			return false, errors.New("compiled code present without a manifest")
		}
		return false, nil // a cold cache
	}
	if err != nil {
		return false, fmt.Errorf("read manifest: %w", err)
	}
	var manifest cacheManifestFile
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return false, fmt.Errorf("manifest unreadable: %w", err)
	}
	for rel, want := range manifest.Entries {
		if !cacheEntryPath.MatchString(rel) {
			return false, fmt.Errorf("manifest lists unexpected file %q", rel)
		}
		got, err := hashFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return false, fmt.Errorf("entry %s: %w", filepath.Base(rel)[:12], err)
		}
		if got != want {
			return false, fmt.Errorf("entry %s changed on disk (size %d, want %d)",
				filepath.Base(rel)[:12], got.Size, want.Size)
		}
	}
	for _, rel := range found {
		if _, ok := manifest.Entries[rel]; !ok {
			_ = os.Remove(filepath.Join(dir, filepath.FromSlash(rel))) // never verified
		}
	}
	return len(manifest.Entries) > 0, nil
}

// scanCache lists the compiled entries (as manifest keys) and deletes
// everything that is neither an entry nor bookkeeping: wazero's .tmp files
// from an interrupted write, our own unfinished writes, anything foreign.
func scanCache(dir string) ([]string, error) {
	top, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var found []string
	for _, e := range top {
		name := e.Name()
		if !e.IsDir() && cacheBookkeeper[name] {
			continue
		}
		if !e.IsDir() || !cacheEntryDir.MatchString(name) {
			_ = os.RemoveAll(filepath.Join(dir, name))
			continue
		}
		inner, err := os.ReadDir(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		for _, f := range inner {
			if !f.IsDir() && cacheEntryName.MatchString(f.Name()) {
				found = append(found, name+"/"+f.Name())
			} else {
				_ = os.RemoveAll(filepath.Join(dir, name, f.Name()))
			}
		}
	}
	return found, nil
}

func hashEntries(dir string) (cacheManifestFile, error) {
	m := cacheManifestFile{Entries: map[string]cacheEntry{}}
	found, err := scanCache(dir)
	if err != nil {
		return m, err
	}
	for _, rel := range found {
		e, err := hashFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return m, err
		}
		m.Entries[rel] = e
	}
	return m, nil
}

func hashFile(path string) (cacheEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return cacheEntry{}, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return cacheEntry{}, err
	}
	return cacheEntry{Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

// wipeCache removes compiled code and the manifest, keeping the failure
// count and the disabled flag.
func wipeCache(dir string) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, f := range files {
		if name := f.Name(); f.IsDir() || !cacheBookkeeper[name] || name == cacheManifest {
			_ = os.RemoveAll(filepath.Join(dir, name))
		}
	}
	syncDir(dir)
}

func removeOtherBuilds(base, keep string, logger *slog.Logger) {
	entries, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Name() != keep && strings.HasPrefix(e.Name(), cacheDirPrefix) {
			if err := os.RemoveAll(filepath.Join(base, e.Name())); err == nil {
				logger.Info("removed pdf renderer cache from another build", "dir", e.Name())
			}
		}
	}
}

func readStrikes(dir string) int {
	raw, err := os.ReadFile(filepath.Join(dir, cacheStrikes))
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	return max(n, 0)
}

// cacheBuildID names the cache folder after everything compiled code
// depends on: the wazero version, the Go version and the platform.
func cacheBuildID() string {
	wazero := "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			if dep.Path == "github.com/tetratelabs/wazero" {
				wazero = dep.Version
				if dep.Replace != nil {
					wazero = dep.Replace.Version + "-replaced"
				}
			}
		}
	}
	id := strings.Join([]string{wazero, runtime.Version(), runtime.GOOS, runtime.GOARCH}, "-")
	return regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(id, "_")
}

func writeJSONSync(path string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return writeFileSync(path, raw)
}

// writeFileSync replaces path atomically: temp file, fsync, rename.
func writeFileSync(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	if werr == nil {
		werr = tmp.Sync()
	}
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), path)
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
	}
	return werr
}

// syncDir flushes a directory's entries (a rename is only durable once its
// directory is), where the platform supports it.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}
