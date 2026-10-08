package metadata_test

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jishnu-Prasad888/Cairn/internal/crypto"
	"github.com/Jishnu-Prasad888/Cairn/internal/metadata"
)

// makeJPEG writes a minimal valid JPEG image to path and returns it.
func makeJPEG(t *testing.T, path string, w, h int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := jpeg.Encode(f, img, nil); err != nil {
		t.Fatal(err)
	}
}

func makePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// --- ExtractFromFile ---

func TestExtractFromFileJPEG(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "photo.jpg")
	makeJPEG(t, path, 800, 600)

	info, err := metadata.ExtractFromFile(path)
	if err != nil {
		t.Fatalf("ExtractFromFile: %v", err)
	}
	if info.Width != 800 || info.Height != 600 {
		t.Errorf("dimensions = %dx%d, want 800x600", info.Width, info.Height)
	}
}

func TestExtractFromFilePNG(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "photo.png")
	makePNG(t, path, 320, 240)

	info, err := metadata.ExtractFromFile(path)
	if err != nil {
		t.Fatalf("ExtractFromFile: %v", err)
	}
	if info.Width != 320 || info.Height != 240 {
		t.Errorf("dimensions = %dx%d, want 320x240", info.Width, info.Height)
	}
}

func TestExtractFromFileNonImage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "readme.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Should not error; just returns zero dimensions.
	info, err := metadata.ExtractFromFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Width != 0 || info.Height != 0 {
		t.Errorf("expected zero dimensions for non-image, got %dx%d", info.Width, info.Height)
	}
}

func TestExtractFromFileDoesNotModifyOriginal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "photo.jpg")
	makeJPEG(t, path, 100, 100)

	statBefore, _ := os.Stat(path)

	if _, err := metadata.ExtractFromFile(path); err != nil {
		t.Fatal(err)
	}

	statAfter, _ := os.Stat(path)
	if statAfter.Size() != statBefore.Size() {
		t.Errorf("file size changed after extraction: %d → %d",
			statBefore.Size(), statAfter.Size())
	}
	if statAfter.ModTime() != statBefore.ModTime() {
		t.Error("file mtime changed after extraction")
	}
}

// --- ExtractFromReader ---

func TestExtractFromReader(t *testing.T) {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 200, 150))
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	info, err := metadata.ExtractFromReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("ExtractFromReader: %v", err)
	}
	if info.Width != 200 || info.Height != 150 {
		t.Errorf("dimensions = %dx%d, want 200x150", info.Width, info.Height)
	}
}

// --- GenerateThumbnail ---

func TestGenerateThumbnailJPEG(t *testing.T) {
	dir := t.TempDir()
	cairnDir := filepath.Join(dir, ".cairn")
	srcPath := filepath.Join(dir, "photo.jpg")
	makeJPEG(t, srcPath, 1200, 900)

	ok, err := metadata.GenerateThumbnail(srcPath, cairnDir, "file-id-1", crypto.NewKeys(""))
	if err != nil {
		t.Fatalf("GenerateThumbnail: %v", err)
	}
	if !ok {
		t.Fatal("expected thumbnail to be generated")
	}

	thumbPath := metadata.ThumbPath(cairnDir, "file-id-1")
	if _, err := os.Stat(thumbPath); err != nil {
		t.Fatalf("thumbnail not on disk: %v", err)
	}

	// Verify thumbnail is a valid JPEG within size limits.
	f, _ := os.Open(thumbPath)
	defer func() { _ = f.Close() }()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatalf("decode thumbnail: %v", err)
	}
	if cfg.Width > metadata.ThumbnailSize || cfg.Height > metadata.ThumbnailSize {
		t.Errorf("thumbnail dimensions %dx%d exceed max %d",
			cfg.Width, cfg.Height, metadata.ThumbnailSize)
	}
}

func TestGenerateThumbnailPNG(t *testing.T) {
	dir := t.TempDir()
	cairnDir := filepath.Join(dir, ".cairn")
	srcPath := filepath.Join(dir, "photo.png")
	makePNG(t, srcPath, 800, 600)

	ok, err := metadata.GenerateThumbnail(srcPath, cairnDir, "file-id-2", crypto.NewKeys(""))
	if err != nil {
		t.Fatalf("GenerateThumbnail: %v", err)
	}
	if !ok {
		t.Fatal("expected PNG thumbnail to be generated")
	}
}

func TestGenerateThumbnailNonImage(t *testing.T) {
	dir := t.TempDir()
	cairnDir := filepath.Join(dir, ".cairn")
	srcPath := filepath.Join(dir, "video.mp4")
	if err := os.WriteFile(srcPath, []byte("not an image"), 0o644); err != nil {
		t.Fatal(err)
	}

	ok, err := metadata.GenerateThumbnail(srcPath, cairnDir, "file-id-3", crypto.NewKeys(""))
	if err != nil {
		t.Fatalf("unexpected error for non-image: %v", err)
	}
	if ok {
		t.Error("should not generate thumbnail for non-image file")
	}
}

func TestGenerateThumbnailDoesNotModifyOriginal(t *testing.T) {
	dir := t.TempDir()
	cairnDir := filepath.Join(dir, ".cairn")
	srcPath := filepath.Join(dir, "photo.jpg")
	makeJPEG(t, srcPath, 600, 400)

	statBefore, _ := os.Stat(srcPath)

	if _, err := metadata.GenerateThumbnail(srcPath, cairnDir, "file-id-4", crypto.NewKeys("")); err != nil {
		t.Fatal(err)
	}

	statAfter, _ := os.Stat(srcPath)
	if statAfter.Size() != statBefore.Size() {
		t.Errorf("original size changed: %d → %d", statBefore.Size(), statAfter.Size())
	}
	if statAfter.ModTime() != statBefore.ModTime() {
		t.Error("original mtime changed after thumbnail generation")
	}
}

func TestThumbnailSmallImageUnchanged(t *testing.T) {
	dir := t.TempDir()
	cairnDir := filepath.Join(dir, ".cairn")
	srcPath := filepath.Join(dir, "tiny.jpg")
	// Image smaller than ThumbnailSize — should still be written as thumbnail.
	makeJPEG(t, srcPath, 100, 100)

	ok, err := metadata.GenerateThumbnail(srcPath, cairnDir, "file-id-5", crypto.NewKeys(""))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("expected thumbnail for small image")
	}

	thumbPath := metadata.ThumbPath(cairnDir, "file-id-5")
	f, _ := os.Open(thumbPath)
	defer func() { _ = f.Close() }()
	cfg, _, _ := image.DecodeConfig(f)
	// Small image should not be upscaled.
	if cfg.Width > metadata.ThumbnailSize || cfg.Height > metadata.ThumbnailSize {
		t.Errorf("small image was upscaled to %dx%d", cfg.Width, cfg.Height)
	}
}

func TestThumbnailEncryptedRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cairnDir := filepath.Join(dir, ".cairn")
	srcPath := filepath.Join(dir, "photo.jpg")
	makeJPEG(t, srcPath, 800, 600)

	keys := crypto.NewKeys("a phrasey passphrase")

	ok, err := metadata.GenerateThumbnail(srcPath, cairnDir, "enc-1", keys)
	if err != nil {
		t.Fatalf("GenerateThumbnail: %v", err)
	}
	if !ok {
		t.Fatal("expected encrypted thumbnail to be generated")
	}

	// On disk the artifact is sealed; it must not be a decodable JPEG.
	thumbPath := metadata.ThumbPath(cairnDir, "enc-1")
	raw, err := os.ReadFile(thumbPath)
	if err != nil {
		t.Fatalf("read sealed thumbnail: %v", err)
	}
	if !crypto.IsSealed(raw) {
		t.Fatal("on-disk thumbnail is not sealed")
	}
	if _, _, err := image.DecodeConfig(bytes.NewReader(raw)); err == nil {
		t.Fatal("sealed thumbnail decodes as a raw JPEG — plaintext on disk")
	}

	// Reading back with the same keys yields a valid, decodable JPEG.
	data, _, err := metadata.ReadThumb(cairnDir, "enc-1", keys)
	if err != nil {
		t.Fatalf("ReadThumb: %v", err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode decrypted thumbnail: %v", err)
	}
	if cfg.Width != 400 || cfg.Height != 300 {
		t.Errorf("decrypted thumbnail = %dx%d, want 400x300", cfg.Width, cfg.Height)
	}

	// The wrong passphrase cannot decrypt it.
	wrong := crypto.NewKeys("nope")
	if _, _, err := metadata.ReadThumb(cairnDir, "enc-1", wrong); err == nil {
		t.Fatal("wrong passphrase read the sealed thumbnail")
	}
}

// --- ThumbPath ---

func TestThumbPath(t *testing.T) {
	p := metadata.ThumbPath("/tmp/.cairn", "abc123")
	if !strings.HasSuffix(p, "abc123.jpg") {
		t.Errorf("ThumbPath = %q, want suffix abc123.jpg", p)
	}
	if !strings.Contains(p, "thumbs") {
		t.Errorf("ThumbPath %q missing 'thumbs' dir", p)
	}
}
