package ml

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func waitModel(t *testing.T, d *ModelDownloader) ModelStatus {
	t.Helper()
	for i := 0; i < 3000; i++ {
		st := d.Status()
		if st.State == ModelDone || st.State == ModelFailed {
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("download did not finish")
	return ModelStatus{}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestModelDownloadRejectsGarbageAndKeepsNothing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("definitely not an onnx model"))
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "models", "m.onnx")
	// verify: try to load as SCRFD detector
	d := NewModelDownloader(testLogger(), path, srv.URL,
		func(part string) error {
			_, err := NewSCRFDDetector(part)
			return err
		},
		nil)
	if d.Installed() {
		t.Fatal("installed before download")
	}
	d.Start()
	if st := waitModel(t, d); st.State != ModelFailed || st.Error == "" {
		t.Fatalf("want failure, got %+v", st)
	}
	if d.Installed() {
		t.Fatal("garbage was installed")
	}
	if _, err := os.Stat(path + ".part"); err == nil {
		t.Fatal("partial file left behind")
	}
}

func TestModelDownloadHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	d := NewModelDownloader(testLogger(),
		filepath.Join(t.TempDir(), "m.onnx"), srv.URL, nil, nil)
	d.Start()
	if st := waitModel(t, d); st.State != ModelFailed {
		t.Fatalf("got %+v", st)
	}
}

func TestModelDownloadInstallsAnyFile(t *testing.T) {
	// Without a verify func, any non-empty file is "installed" successfully.
	// This simulates a pre-verified model delivery.
	data := []byte("stub model data")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	// Use a buffered channel to communicate the path from the onReady callback
	// (called from the downloader goroutine) to the test goroutine without a
	// data race.
	readyCh := make(chan string, 1)
	d := NewModelDownloader(testLogger(),
		filepath.Join(t.TempDir(), "models", "m.onnx"), srv.URL,
		nil, // no verify
		func(p string) { readyCh <- p })
	d.Start()
	st := waitModel(t, d)
	if st.State != ModelDone || !st.Installed || st.Downloaded != int64(len(data)) {
		t.Fatalf("got %+v", st)
	}
	select {
	case gotPath := <-readyCh:
		if gotPath == "" {
			t.Fatal("onReady called with empty path")
		}
	default:
		t.Fatal("onReady not called")
	}
}
