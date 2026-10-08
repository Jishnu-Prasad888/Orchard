package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleGetFileNote_Empty(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)
	if err := seedLibraryFile(t, libRoot, "f1", "beach.jpg", 100); err != nil {
		t.Fatal(err)
	}

	rec := client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files/f1/note", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Note struct {
			FileID string `json:"file_id"`
			Body   string `json:"body"`
		} `json:"note"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Note.FileID != "f1" {
		t.Errorf("file_id = %q, want f1", resp.Note.FileID)
	}
	if resp.Note.Body != "" {
		t.Errorf("body = %q, want empty", resp.Note.Body)
	}
}

func TestHandleSetFileNote_RoundTrip(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)
	if err := seedLibraryFile(t, libRoot, "f1", "beach.jpg", 100); err != nil {
		t.Fatal(err)
	}

	body := "# Beach day\n\nSunset over the **dunes**."
	rec := client.do(t, http.MethodPut, "/api/v1/libraries/"+libID+"/files/f1/note",
		map[string]string{"body": body})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files/f1/note", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Note struct {
			Body      string `json:"body"`
			UpdatedAt string `json:"updated_at"`
		} `json:"note"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Note.Body != body {
		t.Errorf("body = %q, want %q", resp.Note.Body, body)
	}
	if resp.Note.UpdatedAt == "" {
		t.Error("updated_at should be set after saving")
	}
}

func TestHandleClearFileNote_RemovesBody(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)
	if err := seedLibraryFile(t, libRoot, "f1", "beach.jpg", 100); err != nil {
		t.Fatal(err)
	}

	client.do(t, http.MethodPut, "/api/v1/libraries/"+libID+"/files/f1/note",
		map[string]string{"body": "temporary"})

	rec := client.do(t, http.MethodDelete, "/api/v1/libraries/"+libID+"/files/f1/note", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 body=%s", rec.Code, rec.Body.String())
	}

	rec = client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files/f1/note", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Note struct {
			Body string `json:"body"`
		} `json:"note"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Note.Body != "" {
		t.Errorf("body after clear = %q, want empty", resp.Note.Body)
	}
}

func TestHandleGetFileNote_FileNotFound(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)

	rec := client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files/ghost/note", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleSetFileNote_Unauthenticated(t *testing.T) {
	h, _, libID, libRoot := newSearchTestServer(t)
	if err := seedLibraryFile(t, libRoot, "f1", "beach.jpg", 100); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPut,
		"/api/v1/libraries/"+libID+"/files/f1/note",
		bytes.NewBufferString(`{"body":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}
