package httpapi

import (
	"bytes"
	"image"
	"image/jpeg"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/Jishnu-Prasad888/Cairn/internal/crypto"
)

// TestEncryptedThumbnailServesDecryptedJPEG is the end-to-end Phase 13 proof
// at the HTTP layer: with the at-rest key wired into the server, a file whose
// on-disk thumbnail is sealed still produces a decodable JPEG over the API,
// the original media file is left byte-for-byte untouched, and the on-disk
// artifact refuses to decode as plaintext.
func TestEncryptedThumbnailServesDecryptedJPEG(t *testing.T) {
	keys := crypto.NewKeys("test at-rest passphrase")
	_, client, libID, libRoot := newSearchTestServerWithKeys(t, keys)

	// Write a real JPEG on disk and seed its index row so the thumbnail
	// handler can resolve both the file record and the source bytes.
	srcPath := filepath.Join(libRoot, "holiday", "sunset.jpg")
	if err := os.MkdirAll(filepath.Dir(srcPath), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 700, 500))
	if err := jpeg.Encode(f, img, nil); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	before, _ := os.ReadFile(srcPath)

	const fileID = "file-sunset-1"
	if err := seedLibraryFile(t, libRoot, fileID, "holiday/sunset.jpg", int64(len(before))); err != nil {
		t.Fatalf("seed index row: %v", err)
	}

	rec := client.roundTrip(t, http.MethodGet,
		"/api/v1/libraries/"+libID+"/files/"+fileID+"/thumbnail", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("thumbnail status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("content-type = %q, want image/jpeg", ct)
	}
	body := rec.Body.Bytes()
	if !bytes.HasPrefix(body, []byte{0xff, 0xd8, 0xff}) {
		t.Error("served thumbnail is not a JPEG")
	}
	if _, _, err := image.Decode(bytes.NewReader(body)); err != nil {
		t.Errorf("served thumbnail does not decode: %v", err)
	}

	// The artifact on disk is sealed; it must not decode as plaintext.
	thumbPath := filepath.Join(libRoot, ".cairn", "thumbs", fileID+".jpg")
	raw, err := os.ReadFile(thumbPath)
	if err != nil {
		t.Fatalf("read on-disk thumbnail: %v", err)
	}
	if !crypto.IsSealed(raw) {
		t.Error("on-disk thumbnail is not sealed with the at-rest key")
	}
	if _, _, err := image.Decode(bytes.NewReader(raw)); err == nil {
		t.Error("on-disk sealed thumbnail decodes as plaintext JPEG")
	}

	// The original media file is byte-for-byte untouched (Phase 13 scope).
	after, _ := os.ReadFile(srcPath)
	if !bytes.Equal(before, after) {
		t.Error("original media file was modified by thumbnail generation")
	}
}
