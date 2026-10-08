package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestHandleUpdateAlbum(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)

	create := client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/albums",
		map[string]string{"name": "Holiday"})
	var cr struct {
		Album struct {
			ID string `json:"id"`
		} `json:"album"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &cr); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}

	rec := client.do(t, http.MethodPatch, "/api/v1/libraries/"+libID+"/albums/"+cr.Album.ID,
		map[string]string{"name": "Summer 2026", "description": "By the sea"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Album struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			FileCount   int    `json:"file_count"`
		} `json:"album"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Album.Name != "Summer 2026" || resp.Album.Description != "By the sea" {
		t.Errorf("album = %+v", resp.Album)
	}
	if resp.Album.FileCount != 0 {
		t.Errorf("file_count = %d, want 0", resp.Album.FileCount)
	}
}

func TestHandleUpdateAlbum_EmptyName(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)
	create := client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/albums",
		map[string]string{"name": "Holiday"})
	var cr struct {
		Album struct {
			ID string `json:"id"`
		} `json:"album"`
	}
	_ = json.Unmarshal(create.Body.Bytes(), &cr)

	rec := client.do(t, http.MethodPatch, "/api/v1/libraries/"+libID+"/albums/"+cr.Album.ID,
		map[string]string{"name": ""})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleUpdateAlbum_NotFound(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)
	rec := client.do(t, http.MethodPatch, "/api/v1/libraries/"+libID+"/albums/ghost",
		map[string]string{"name": "x"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
