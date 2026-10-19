package bookmeta

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klippa-app/go-pdfium"
)

var quietLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// fakeEntry is laid out the way wazero stores a compiled module.
const fakeEntry = "wazero-test-amd64-linux/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func writeEntry(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func cacheDirOf(t *testing.T, base string) string {
	t.Helper()
	return filepath.Join(base, cacheDirPrefix+cacheBuildID())
}

// sealedCache prepares a cache holding one fake compiled entry and seals it,
// as a run that executed the renderer and survived would.
func sealedCache(t *testing.T) (base, dir string) {
	t.Helper()
	base = t.TempDir()
	c := prepareRendererCache(base, quietLogger)
	if c == nil {
		t.Fatal("no cache")
	}
	writeEntry(t, c.dir, fakeEntry, "compiled code")
	c.confirmed()
	return base, c.dir
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestRendererCacheVerifiesBeforeUse(t *testing.T) {
	t.Run("a sealed cache is used warm", func(t *testing.T) {
		base, dir := sealedCache(t)
		c := prepareRendererCache(base, quietLogger)
		if c == nil || !c.warm || !exists(filepath.Join(dir, filepath.FromSlash(fakeEntry))) {
			t.Fatalf("sealed cache not reused warm: %+v", c)
		}
	})
	for name, damage := range map[string]func(dir string){
		"a flipped byte": func(dir string) {
			writeEntry(t, dir, fakeEntry, "compiled cod3")
		},
		"a truncated entry": func(dir string) {
			_ = os.Truncate(filepath.Join(dir, filepath.FromSlash(fakeEntry)), 4)
		},
		"an unreadable manifest": func(dir string) {
			_ = os.WriteFile(filepath.Join(dir, cacheManifest), []byte("{garbage"), 0o600)
		},
		"a missing manifest": func(dir string) {
			_ = os.Remove(filepath.Join(dir, cacheManifest))
		},
		"a missing entry": func(dir string) {
			_ = os.Remove(filepath.Join(dir, filepath.FromSlash(fakeEntry)))
		},
	} {
		t.Run(name+" wipes it", func(t *testing.T) {
			base, dir := sealedCache(t)
			damage(dir)
			c := prepareRendererCache(base, quietLogger)
			if c == nil || c.warm {
				t.Fatalf("damaged cache used warm: %+v", c)
			}
			if exists(filepath.Join(dir, filepath.FromSlash(fakeEntry))) || exists(filepath.Join(dir, cacheManifest)) {
				t.Fatal("damaged cache not wiped")
			}
		})
	}
	t.Run("unlisted files and interrupted writes are removed, listed ones kept", func(t *testing.T) {
		base, dir := sealedCache(t)
		other := "wazero-test-amd64-linux/" + strings.Repeat("f", 64)
		strays := []string{other, fakeEntry + ".123.tmp", ".write-9", "stray-dir/x"}
		for _, name := range strays {
			writeEntry(t, dir, name, "x")
		}
		c := prepareRendererCache(base, quietLogger)
		if c == nil || !c.warm {
			t.Fatalf("cache = %+v", c)
		}
		for _, name := range strays {
			if exists(filepath.Join(dir, filepath.FromSlash(name))) {
				t.Errorf("%s survived", name)
			}
		}
		if !exists(filepath.Join(dir, filepath.FromSlash(fakeEntry))) {
			t.Error("listed entry removed")
		}
	})
	t.Run("other builds' caches are removed", func(t *testing.T) {
		base := t.TempDir()
		old := filepath.Join(base, cacheDirPrefix+"v1.0.0-go1.20-linux-arm64")
		_ = os.MkdirAll(old, 0o700)
		unrelated := filepath.Join(base, "something-else")
		_ = os.MkdirAll(unrelated, 0o700)
		prepareRendererCache(base, quietLogger)
		if exists(old) || !exists(unrelated) {
			t.Fatalf("old build kept: %v, unrelated removed: %v", exists(old), !exists(unrelated))
		}
	})
}

func TestRendererCacheCannotCauseACrashLoop(t *testing.T) {
	base, dir := sealedCache(t)

	// A start that loads the cache and dies before confirming.
	if c := prepareRendererCache(base, quietLogger); c == nil || !c.warm {
		t.Fatal("expected a warm start")
	}
	// The next start sees the warm marker: one strike, cache rebuilt cold.
	c := prepareRendererCache(base, quietLogger)
	if c == nil || c.warm || readStrikes(dir) != 1 {
		t.Fatalf("after one death: cache %+v, strikes %d", c, readStrikes(dir))
	}
	// That cold run compiles and confirms; a cold run proves nothing about
	// cached code, so the strike stays.
	writeEntry(t, dir, fakeEntry, "compiled code")
	c.confirmed()
	if readStrikes(dir) != 1 {
		t.Fatalf("cold confirm cleared strikes: %d", readStrikes(dir))
	}
	// Warm again, and dies again: the cache is turned off for this build.
	if c := prepareRendererCache(base, quietLogger); c == nil || !c.warm {
		t.Fatal("expected a warm start")
	}
	if c := prepareRendererCache(base, quietLogger); c != nil {
		t.Fatal("cache still used after repeated deaths")
	}
	if c := prepareRendererCache(base, quietLogger); c != nil || !exists(filepath.Join(dir, cacheDisabled)) {
		t.Fatal("disabled cache came back")
	}
	if exists(filepath.Join(dir, filepath.FromSlash(fakeEntry))) || exists(filepath.Join(dir, cacheMarker)) {
		t.Fatal("disabled cache kept compiled code or its marker")
	}
}

func TestRendererCacheForgivesColdDeathsAndCleanWarmRuns(t *testing.T) {
	base := t.TempDir()
	// A cold start killed mid-compile (shutdown, power cut) is no strike.
	if c := prepareRendererCache(base, quietLogger); c == nil || c.warm {
		t.Fatal("expected a cold start")
	}
	c := prepareRendererCache(base, quietLogger)
	dir := cacheDirOf(t, base)
	if c == nil || readStrikes(dir) != 0 {
		t.Fatalf("cold death counted: strikes %d", readStrikes(dir))
	}
	// One warm death, then a clean warm run, clears the record.
	writeEntry(t, dir, fakeEntry, "code")
	c.confirmed()
	prepareRendererCache(base, quietLogger) // warm, dies
	c = prepareRendererCache(base, quietLogger)
	writeEntry(t, dir, fakeEntry, "code")
	c.confirmed()
	c = prepareRendererCache(base, quietLogger)
	if c == nil || !c.warm {
		t.Fatal("expected a warm start")
	}
	c.confirmed()
	if readStrikes(dir) != 0 {
		t.Fatalf("clean warm run left strikes: %d", readStrikes(dir))
	}
}

// --- with the real renderer ---

func renderWith(t *testing.T, pool pdfium.Pool) {
	t.Helper()
	instance, err := pool.GetInstance(30 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = instance.Close() }()
	img, err := renderLeadingPage(instance, buildTestPDFWithContent(t, "0 0 0 rg 0 0 200 200 re f"))
	if err != nil || img == nil {
		t.Fatalf("render: %v", err)
	}
}

func startAndRender(t *testing.T, base string) (*rendererCache, time.Duration) {
	t.Helper()
	start := time.Now()
	pool, cache, err := startPDFium(base, quietLogger)
	if err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	defer func() { _ = pool.Close() }()
	renderWith(t, pool)
	cache.confirmed()
	return cache, took
}

// compiledEntry is the single compiled module wazero cached.
func compiledEntry(t *testing.T, dir string) string {
	t.Helper()
	found, err := scanCache(dir)
	if err != nil || len(found) != 1 {
		t.Fatalf("compiled entries = %v, %v", found, err)
	}
	return filepath.Join(dir, filepath.FromSlash(found[0]))
}

func TestRendererCacheWithPDFium(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles PDFium several times")
	}
	base := t.TempDir()
	dir := cacheDirOf(t, base)

	cold, coldTook := startAndRender(t, base)
	if cold == nil || cold.warm {
		t.Fatal("first start should be cold")
	}
	warm, warmTook := startAndRender(t, base)
	if warm == nil || !warm.warm {
		t.Fatal("second start should be warm")
	}
	t.Logf("cold start %v, warm start %v", coldTook, warmTook)
	if warmTook*4 > coldTook {
		t.Errorf("warm start (%v) not much faster than cold (%v)", warmTook, coldTook)
	}

	// Bit rot on the card: verification catches it, PDFium compiles afresh.
	entry := compiledEntry(t, dir)
	raw, _ := os.ReadFile(entry)
	raw[len(raw)/2] ^= 0xff
	_ = os.WriteFile(entry, raw, 0o600)
	if c, _ := startAndRender(t, base); c == nil || c.warm {
		t.Fatal("corrupted cache was loaded")
	}

	// Damage our check can't see (the manifest re-sealed over a bad entry):
	// wazero refuses it, and the renderer still starts, uncached.
	entry = compiledEntry(t, dir)
	raw, _ = os.ReadFile(entry)
	raw[0] ^= 0xff // wazero's header magic
	_ = os.WriteFile(entry, raw, 0o600)
	m, err := hashEntries(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONSync(filepath.Join(dir, cacheManifest), m); err != nil {
		t.Fatal(err)
	}
	pool, cache, err := startPDFium(base, quietLogger)
	if err != nil {
		t.Fatalf("renderer failed to start over a refused cache: %v", err)
	}
	renderWith(t, pool)
	_ = pool.Close()
	if cache != nil {
		t.Fatal("refused cache still in use")
	}
	if _, err := os.Stat(entry); err == nil {
		t.Fatal("refused cache not wiped")
	}
	// And the next start builds a good one again.
	if c, _ := startAndRender(t, base); c == nil {
		t.Fatal("cache not rebuilt")
	}
	if c, _ := startAndRender(t, base); c == nil || !c.warm {
		t.Fatal("rebuilt cache not reused")
	}
}
