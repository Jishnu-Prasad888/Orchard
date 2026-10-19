package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

type uploadBody struct {
	Upload struct {
		ID       string `json:"id"`
		Offset   int64  `json:"offset"`
		Size     int64  `json:"size"`
		Complete bool   `json:"complete"`
		File     struct {
			ID string `json:"id"`
		} `json:"file"`
	} `json:"upload"`
	File struct {
		ID string `json:"id"`
	} `json:"file"`
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

func decodeUpload(t *testing.T, rec *httptest.ResponseRecorder) uploadBody {
	t.Helper()
	var b uploadBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return b
}

// sendChunk PATCHes raw bytes at offset, the way the web client does.
func sendChunk(t *testing.T, c *testClient, url string, offset int64, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, url, body)
	req.Header.Set("Content-Type", "application/offset+octet-stream")
	req.Header.Set("Upload-Offset", strconv.FormatInt(offset, 10))
	for name, value := range c.cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	rec := httptest.NewRecorder()
	c.handler.ServeHTTP(rec, req)
	return rec
}

// brokenReader yields some bytes and then fails, like a dropped connection.
type brokenReader struct {
	data string
	sent bool
}

func (b *brokenReader) Read(p []byte) (int, error) {
	if b.sent {
		return 0, io.ErrUnexpectedEOF
	}
	b.sent = true
	return copy(p, b.data), nil
}

func TestResumableUploadSurvivesADroppedChunk(t *testing.T) {
	_, admin, _, libID := newAuthzTestServer(t)
	base := "/api/v1/libraries/" + libID + "/uploads"
	content := "hello orchard, chunk by chunk"

	rec := admin.do(t, http.MethodPost, base,
		map[string]any{"path": "books/a.txt", "size": len(content)})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	url := base + "/" + decodeUpload(t, rec).Upload.ID

	// The connection drops after 5 bytes; those 5 are kept.
	rec = sendChunk(t, admin, url, 0, &brokenReader{data: content[:5]})
	if got := decodeUpload(t, rec).Error.Code; rec.Code != http.StatusBadRequest || got != CodeUploadInterrupted {
		t.Fatalf("broken chunk = %d %s", rec.Code, rec.Body)
	}
	rec = admin.do(t, http.MethodGet, url, nil)
	if off := decodeUpload(t, rec).Upload.Offset; off != 5 {
		t.Fatalf("offset after drop = %d, want 5", off)
	}

	// A retry of the whole chunk from 0 is told where to resume.
	rec = sendChunk(t, admin, url, 0, strings.NewReader(content[:10]))
	b := decodeUpload(t, rec)
	if rec.Code != http.StatusConflict || b.Error.Code != CodeUploadOffsetMismatch || b.Error.Details["offset"] != float64(5) {
		t.Fatalf("stale offset = %d %s", rec.Code, rec.Body)
	}

	// Finishing early is refused with how much has arrived.
	rec = admin.do(t, http.MethodPost, url+"/complete", nil)
	if b := decodeUpload(t, rec); rec.Code != http.StatusConflict || b.Error.Code != CodeUploadIncomplete {
		t.Fatalf("early complete = %d %s", rec.Code, rec.Body)
	}

	rec = sendChunk(t, admin, url, 5, strings.NewReader(content[5:]))
	if rec.Code != http.StatusOK || decodeUpload(t, rec).Upload.Offset != int64(len(content)) {
		t.Fatalf("final chunk = %d %s", rec.Code, rec.Body)
	}

	rec = admin.do(t, http.MethodPost, url+"/complete", nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("complete = %d %s", rec.Code, rec.Body)
	}
	fileID := decodeUpload(t, rec).File.ID

	// A lost "complete" response is safe to ask again.
	rec = admin.do(t, http.MethodPost, url+"/complete", nil)
	if rec.Code != http.StatusOK || decodeUpload(t, rec).File.ID != fileID {
		t.Fatalf("repeat complete = %d %s", rec.Code, rec.Body)
	}
	rec = admin.do(t, http.MethodGet, url, nil)
	if b := decodeUpload(t, rec); !b.Upload.Complete || b.Upload.File.ID != fileID {
		t.Fatalf("get after complete = %s", rec.Body)
	}

	rec = admin.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files/"+fileID+"/download", nil)
	if rec.Body.String() != content {
		t.Fatalf("downloaded %q, want %q", rec.Body.String(), content)
	}
}

func TestResumableUploadRefusalsExplainThemselves(t *testing.T) {
	_, admin, _, libID := newAuthzTestServer(t)
	base := "/api/v1/libraries/" + libID + "/uploads"
	uploadFile(t, admin, libID, "taken.pdf")

	rec := admin.do(t, http.MethodPost, base, map[string]any{"path": "taken.pdf", "size": 3})
	if b := decodeUpload(t, rec); rec.Code != http.StatusConflict || !strings.Contains(b.Error.Message, "“taken.pdf” already exists") {
		t.Fatalf("existing = %d %s", rec.Code, rec.Body)
	}

	rec = admin.do(t, http.MethodPost, base, map[string]any{"path": "huge.iso", "size": int64(3) << 40})
	if b := decodeUpload(t, rec); rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(b.Error.Message, "upload limit") {
		t.Fatalf("too large = %d %s", rec.Code, rec.Body)
	}

	rec = admin.do(t, http.MethodPost, base, map[string]any{"path": "../escape.txt", "size": 3})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("traversal = %d %s", rec.Code, rec.Body)
	}

	rec = admin.do(t, http.MethodGet, base+"/0123456789abcdef0123456789abcdef", nil)
	if b := decodeUpload(t, rec); rec.Code != http.StatusNotFound || b.Error.Code != CodeUploadNotFound {
		t.Fatalf("unknown = %d %s", rec.Code, rec.Body)
	}
}

func TestResumableUploadCancel(t *testing.T) {
	_, admin, _, libID := newAuthzTestServer(t)
	base := "/api/v1/libraries/" + libID + "/uploads"
	rec := admin.do(t, http.MethodPost, base, map[string]any{"path": "x.bin", "size": 4})
	url := base + "/" + decodeUpload(t, rec).Upload.ID

	if rec := admin.do(t, http.MethodDelete, url, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body)
	}
	if rec := sendChunk(t, admin, url, 0, strings.NewReader("abcd")); rec.Code != http.StatusNotFound {
		t.Fatalf("chunk after cancel = %d %s", rec.Code, rec.Body)
	}
}

// trickle writes data a byte at a time, pausing between bytes, then closes.
func trickle(w *io.PipeWriter, data string, gap time.Duration) {
	for i := range len(data) {
		time.Sleep(gap)
		if _, err := w.Write([]byte{data[i]}); err != nil {
			return
		}
	}
	_ = w.Close()
}

// The original failure: a body that took longer than the server's
// ReadTimeout to arrive was cut off mid-upload. Over a real connection, a
// slow but steady chunk must now finish, and only a true stall is dropped.
func TestChunkOutlivesReadTimeoutButNotAStall(t *testing.T) {
	handler, admin, _, libID := newAuthzTestServer(t)
	srv := httptest.NewUnstartedServer(handler)
	srv.Config.ReadTimeout = 300 * time.Millisecond
	srv.Start()
	defer srv.Close()
	old := uploadStallTimeout
	uploadStallTimeout = 400 * time.Millisecond
	defer func() { uploadStallTimeout = old }()

	base := "/api/v1/libraries/" + libID + "/uploads"
	content := "0123456789"
	rec := admin.do(t, http.MethodPost, base, map[string]any{"path": "slow.txt", "size": 20})
	url := srv.URL + base + "/" + decodeUpload(t, rec).Upload.ID

	patch := func(offset int64, body io.Reader, length int64) (*http.Response, uploadBody) {
		req, _ := http.NewRequest(http.MethodPatch, url, body)
		req.ContentLength = length
		req.Header.Set("Upload-Offset", strconv.FormatInt(offset, 10))
		for name, value := range admin.cookies {
			req.AddCookie(&http.Cookie{Name: name, Value: value})
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("patch: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		var b uploadBody
		_ = json.NewDecoder(resp.Body).Decode(&b)
		return resp, b
	}

	// ~1s in total, well past the 300ms ReadTimeout, never idle for 400ms.
	pr, pw := io.Pipe()
	go trickle(pw, content, 100*time.Millisecond)
	resp, b := patch(0, pr, int64(len(content)))
	if resp.StatusCode != http.StatusOK || b.Upload.Offset != 10 {
		t.Fatalf("slow chunk = %d %+v", resp.StatusCode, b)
	}

	// Two bytes, then silence: the server gives up and keeps the two bytes.
	pr, pw = io.Pipe()
	go func() {
		_, _ = pw.Write([]byte("ab"))
		time.Sleep(2 * time.Second)
		_ = pw.Close()
	}()
	resp, b = patch(10, pr, 10)
	if resp.StatusCode != http.StatusRequestTimeout || b.Error.Code != CodeUploadStalled {
		t.Fatalf("stalled chunk = %d %+v", resp.StatusCode, b)
	}
	if got := decodeUpload(t, admin.do(t, http.MethodGet, strings.TrimPrefix(url, srv.URL), nil)).Upload.Offset; got != 12 {
		t.Fatalf("offset after stall = %d, want 12", got)
	}
}
