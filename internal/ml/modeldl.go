package ml

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Default URLs for the SCRFD-500MF detector (buffalo_s) and ArcFace R50 recognizer, as
// redistributed by Immich from the InsightFace model zoo (buffalo_l pack).
// These models are licensed for non-commercial research use unless a separate
// commercial license is obtained from DeepGlint-AI.
const (
	DefaultFaceDetectorURL   = "https://huggingface.co/immich-app/buffalo_s/resolve/main/detection/model.onnx"
	DefaultFaceRecognizerURL = "https://huggingface.co/immich-app/buffalo_l/resolve/main/recognition/model.onnx"

	// DefaultFaceModelURL is kept for back-compat but now points to the
	// recognizer. New code should use DefaultFaceRecognizerURL.
	DefaultFaceModelURL = DefaultFaceRecognizerURL
)

// ModelState is where a model download stands.
type ModelState string

const (
	ModelIdle        ModelState = "idle"
	ModelDownloading ModelState = "downloading"
	ModelDone        ModelState = "done"
	ModelFailed      ModelState = "error"
)

// ModelStatus is a snapshot for the API.
type ModelStatus struct {
	Installed  bool       `json:"installed"`
	State      ModelState `json:"state"`
	Downloaded int64      `json:"downloaded"`
	Total      int64      `json:"total"` // 0 when the server did not say
	Error      string     `json:"error,omitempty"`
	URL        string     `json:"url"`
}

// ModelDownloader fetches one ONNX model on request and hands it to an
// onReady callback once it has been verified. The download runs in the
// background and is independent of any browser session.
//
// Two downloaders are used in production: one for the SCRFD detector
// (face-detector.onnx) and one for the ArcFace recognizer
// (face-recognition.onnx). They are independent: either can be re-downloaded
// without affecting the other.
type ModelDownloader struct {
	logger  *slog.Logger
	path    string
	url     string
	verify  func(path string) error // nil → accept any valid file
	onReady func(path string)

	mu         sync.Mutex
	state      ModelState
	downloaded int64
	total      int64
	err        string
}

// NewModelDownloader manages the model at path. onReady is called (with the
// installed path) once the downloaded model has been verified. verify, when
// non-nil, is called with the temporary .part path before it is renamed into
// place; return a non-nil error to reject the file.
func NewModelDownloader(
	logger *slog.Logger, path, url string,
	verify func(string) error,
	onReady func(string),
) *ModelDownloader {
	return &ModelDownloader{
		logger:  logger,
		path:    path,
		url:     url,
		verify:  verify,
		onReady: onReady,
		state:   ModelIdle,
	}
}

// Path is where the model file lives (or will).
func (d *ModelDownloader) Path() string { return d.path }

// Installed reports whether a model file is present and non-empty.
func (d *ModelDownloader) Installed() bool {
	st, err := os.Stat(d.path)
	return err == nil && st.Size() > 0
}

// Status returns the current download state.
func (d *ModelDownloader) Status() ModelStatus {
	d.mu.Lock()
	defer d.mu.Unlock()
	return ModelStatus{
		Installed: d.Installed(), State: d.state,
		Downloaded: d.downloaded, Total: d.total, Error: d.err, URL: d.url,
	}
}

// Start begins a background download unless one is running or the model is
// already installed. It returns immediately.
func (d *ModelDownloader) Start() ModelStatus {
	d.mu.Lock()
	if d.state == ModelDownloading || d.Installed() {
		d.mu.Unlock()
		return d.Status()
	}
	d.state, d.downloaded, d.total, d.err = ModelDownloading, 0, 0, ""
	d.mu.Unlock()
	go d.run()
	return d.Status()
}

func (d *ModelDownloader) fail(err error) {
	d.logger.Error("model download failed", "error", err, "path", d.path)
	d.mu.Lock()
	d.state, d.err = ModelFailed, err.Error()
	d.mu.Unlock()
}

func (d *ModelDownloader) run() {
	d.logger.Info("downloading model", "url", d.url, "path", d.path)
	if err := os.MkdirAll(filepath.Dir(d.path), 0o755); err != nil {
		d.fail(err)
		return
	}
	part := d.path + ".part"
	defer func() { _ = os.Remove(part) }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.url, nil)
	if err != nil {
		d.fail(err)
		return
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		d.fail(fmt.Errorf("could not reach the download server: %w", err))
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		d.fail(fmt.Errorf("download server answered %s", resp.Status))
		return
	}
	d.mu.Lock()
	d.total = max(resp.ContentLength, 0)
	d.mu.Unlock()

	f, err := os.Create(part)
	if err != nil {
		d.fail(err)
		return
	}
	pr := &progressReader{r: resp.Body, onRead: func(n int) {
		d.mu.Lock()
		d.downloaded += int64(n)
		d.mu.Unlock()
	}}
	stop := pr.watchdog(cancel, 60*time.Second)
	_, copyErr := io.Copy(f, pr)
	stop()
	closeErr := f.Close()
	switch {
	case copyErr != nil:
		d.fail(fmt.Errorf("download interrupted: %w", copyErr))
		return
	case closeErr != nil:
		d.fail(closeErr)
		return
	}
	d.mu.Lock()
	complete := d.total == 0 || d.downloaded == d.total
	d.mu.Unlock()
	if !complete {
		d.fail(errors.New("download was cut short"))
		return
	}

	// Verify before installing.
	if d.verify != nil {
		if err := d.verify(part); err != nil {
			d.fail(fmt.Errorf("the downloaded file is not a usable model: %w", err))
			return
		}
	}
	if err := os.Rename(part, d.path); err != nil {
		d.fail(err)
		return
	}
	d.mu.Lock()
	d.state = ModelDone
	d.mu.Unlock()
	d.logger.Info("model installed", "path", d.path)
	if d.onReady != nil {
		d.onReady(d.path)
	}
}

// progressReader reports bytes read and lets a watchdog notice a stall.
type progressReader struct {
	r      io.Reader
	onRead func(int)
	mu     sync.Mutex
	last   time.Time
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.mu.Lock()
		p.last = time.Now()
		p.mu.Unlock()
		p.onRead(n)
	}
	return n, err
}

// watchdog cancels the request when no bytes arrive for idle.
func (p *progressReader) watchdog(cancel context.CancelFunc, idle time.Duration) (stop func()) {
	p.mu.Lock()
	p.last = time.Now()
	p.mu.Unlock()
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				p.mu.Lock()
				stalled := time.Since(p.last) > idle
				p.mu.Unlock()
				if stalled {
					cancel()
					return
				}
			}
		}
	}()
	return func() { close(done) }
}
