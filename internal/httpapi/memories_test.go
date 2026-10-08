package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleMemories_Lifecycle(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)

	// Create.
	rec := client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/memories",
		map[string]string{"title": "Summer 2023", "body": "We visited [[media:somefile|the cove]] in June."})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Memory memoryResponse
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}
	if created.Memory.ID == "" || created.Memory.Title != "Summer 2023" {
		t.Fatalf("created memory = %+v", created.Memory)
	}
	memID := created.Memory.ID

	// Update (autosave).
	rec = client.do(t, http.MethodPut, "/api/v1/libraries/"+libID+"/memories/"+memID,
		map[string]string{"title": "Summer 2023 (revised)", "body": "Now with [[album:alb1]]."})
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", rec.Code, rec.Body.String())
	}

	// Read back.
	rec = client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/memories/"+memID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Memory memoryResponse
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal get: %v", err)
	}
	if got.Memory.Title != "Summer 2023 (revised)" {
		t.Errorf("title = %q", got.Memory.Title)
	}

	// Refs were extracted from the latest body.
	rec = client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/memories/"+memID+"/refs", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("refs status = %d", rec.Code)
	}
	var refsResp struct {
		Refs []memoryRefResponse
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &refsResp); err != nil {
		t.Fatalf("unmarshal refs: %v", err)
	}
	if len(refsResp.Refs) != 1 || refsResp.Refs[0].Type != "album" || refsResp.Refs[0].ID != "alb1" {
		t.Errorf("refs = %+v, want single album:alb1 ref", refsResp.Refs)
	}

	// Versions.
	rec = client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/memories/"+memID+"/versions", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("versions status = %d", rec.Code)
	}
	var versionsResp struct {
		Versions []memoryVersionResponse
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &versionsResp); err != nil {
		t.Fatalf("unmarshal versions: %v", err)
	}
	if len(versionsResp.Versions) != 2 {
		t.Errorf("versions = %d, want 2 (create + update)", len(versionsResp.Versions))
	}

	// Delete (soft).
	rec = client.do(t, http.MethodDelete, "/api/v1/libraries/"+libID+"/memories/"+memID, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", rec.Code)
	}
	// Gone from the list.
	rec = client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/memories", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	var listResp struct {
		Memories []memoryResponse
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(listResp.Memories) != 0 {
		t.Errorf("list after delete = %d, want 0", len(listResp.Memories))
	}

	// Restore.
	rec = client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/memories/"+memID+"/restore", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("restore status = %d", rec.Code)
	}
	rec = client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/memories", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal list after restore: %v", err)
	}
	if len(listResp.Memories) != 1 {
		t.Errorf("list after restore = %d, want 1", len(listResp.Memories))
	}
}

func TestHandleListMemories_Search(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)

	client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/memories",
		map[string]string{"title": "Beach day", "body": "Sandy cove in June."})
	client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/memories",
		map[string]string{"title": "City trip", "body": "Museums and espresso."})

	rec := client.do(t, http.MethodGet,
		"/api/v1/libraries/"+libID+"/memories?q=sandy", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("search status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Memories []memoryResponse
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Memories) != 1 || resp.Memories[0].Title != "Beach day" {
		t.Errorf("search results = %+v, want the beach memory", resp.Memories)
	}
}

func TestHandleCreateMemory_Validation(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)

	rec := client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/memories",
		map[string]string{"title": "", "body": "no title"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("blank title status = %d, want 400", rec.Code)
	}
}

func TestHandleGetMemory_NotFound(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)

	rec := client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/memories/ghost", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("get ghost status = %d, want 404", rec.Code)
	}
}

func TestHandleMemories_Unauthenticated(t *testing.T) {
	h, _, libID, _ := newSearchTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/libraries/"+libID+"/memories", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated status = %d, want 401", rec.Code)
	}
}
