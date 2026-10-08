package metadata_test

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jishnu-Prasad888/Cairn/internal/crypto"
	"github.com/Jishnu-Prasad888/Cairn/internal/metadata"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestStorePosterResizesAndKeepsExisting(t *testing.T) {
	cairnDir := filepath.Join(t.TempDir(), ".cairn")
	keys := crypto.NewKeys("")

	stored, err := metadata.StorePoster(bytes.NewReader(pngBytes(t, 1920, 1080)), cairnDir, "vid-1", keys)
	if err != nil || !stored {
		t.Fatalf("StorePoster = %v, %v; want stored", stored, err)
	}
	data, _, err := metadata.ReadThumb(cairnDir, "vid-1", keys)
	if err != nil {
		t.Fatalf("ReadThumb: %v", err)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode stored poster: %v", err)
	}
	if format != "jpeg" || cfg.Width != metadata.ThumbnailSize {
		t.Errorf("stored poster = %s %dx%d, want jpeg %d wide", format, cfg.Width, cfg.Height, metadata.ThumbnailSize)
	}

	// A second poster never replaces the first.
	stored, err = metadata.StorePoster(bytes.NewReader(pngBytes(t, 10, 10)), cairnDir, "vid-1", keys)
	if err != nil || stored {
		t.Fatalf("second StorePoster = %v, %v; want kept", stored, err)
	}
}

func TestStorePosterRejectsNonImages(t *testing.T) {
	cairnDir := filepath.Join(t.TempDir(), ".cairn")
	if _, err := metadata.StorePoster(strings.NewReader("<svg/>"), cairnDir, "vid-2", crypto.NewKeys("")); err == nil {
		t.Fatal("expected an error for a non-image body")
	}
	if _, err := os.Stat(metadata.ThumbPath(cairnDir, "vid-2")); !os.IsNotExist(err) {
		t.Fatalf("nothing should be written, stat err = %v", err)
	}
}

func TestGenerateThumbnailVideoWithoutFFmpeg(t *testing.T) {
	if metadata.VideoFramesAvailable() {
		t.Skip("ffmpeg is installed; this covers the fallback path")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(src, []byte("not really a video"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok, err := metadata.GenerateThumbnail(src, filepath.Join(dir, ".cairn"), "vid-3", crypto.NewKeys(""))
	if err != nil || ok {
		t.Fatalf("GenerateThumbnail = %v, %v; want no thumbnail and no error", ok, err)
	}
}
