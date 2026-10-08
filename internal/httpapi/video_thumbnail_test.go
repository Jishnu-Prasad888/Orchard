package httpapi

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/Jishnu-Prasad888/Cairn/internal/crypto"
	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
	"github.com/Jishnu-Prasad888/Cairn/internal/metadata"
)

// TestVideoPosterUpload covers the browser fallback for servers without
// ffmpeg: a captured first frame becomes the video's thumbnail, photos refuse
// one, and the stored thumbnail is served afterwards.
func TestVideoPosterUpload(t *testing.T) {
	if metadata.VideoFramesAvailable() {
		t.Skip("ffmpeg is installed; the server makes its own video thumbnails")
	}
	_, client, libID, libRoot := newSearchTestServerWithKeys(t, crypto.NewKeys(""))

	if err := os.WriteFile(filepath.Join(libRoot, "clip.mp4"), []byte("video bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := seedLibraryFile(t, libRoot, "vid-1", "clip.mp4", 11); err != nil {
		t.Fatal(err)
	}
	if err := seedLibraryFile(t, libRoot, "pic-1", "pic.jpg", 1); err != nil {
		t.Fatal(err)
	}
	ldb, err := librarydb.Open(filepath.Join(libRoot, ".cairn"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ldb.Exec(`UPDATE indexed_files SET media_type = CASE id WHEN 'vid-1' THEN 'video' ELSE 'photo' END`); err != nil {
		t.Fatal(err)
	}
	_ = ldb.Close()

	base := "/api/v1/libraries/" + libID + "/files/"
	if rec := client.roundTrip(t, http.MethodGet, base+"vid-1/thumbnail", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("thumbnail before poster = %d, want 404", rec.Code)
	}

	var frame bytes.Buffer
	if err := png.Encode(&frame, image.NewRGBA(image.Rect(0, 0, 640, 360))); err != nil {
		t.Fatal(err)
	}
	if rec := client.roundTrip(t, http.MethodPut, base+"pic-1/thumbnail", frame.String()); rec.Code != http.StatusBadRequest {
		t.Fatalf("poster for a photo = %d, want 400", rec.Code)
	}
	if rec := client.roundTrip(t, http.MethodPut, base+"vid-1/thumbnail", "not an image"); rec.Code != http.StatusBadRequest {
		t.Fatalf("garbage poster = %d, want 400", rec.Code)
	}
	rec := client.roundTrip(t, http.MethodPut, base+"vid-1/thumbnail", frame.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("poster upload = %d (%s), want 200", rec.Code, rec.Body.String())
	}

	rec = client.roundTrip(t, http.MethodGet, base+"vid-1/thumbnail", "")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("thumbnail after poster = %d %q, want 200 image/jpeg", rec.Code, rec.Header().Get("Content-Type"))
	}
}
