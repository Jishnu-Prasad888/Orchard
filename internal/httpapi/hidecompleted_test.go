package httpapi

import (
	"net/http"
	"testing"
)

func TestHideCompleted_DefaultsOffThenRoundTrips(t *testing.T) {
	_, client, _, _ := newSearchTestServer(t)

	var body hideCompletedBody
	rec := client.do(t, http.MethodGet, "/api/v1/settings/hide-completed", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, body=%s", rec.Code, rec.Body.String())
	}
	jsonUnmarshalRec(t, rec, &body)
	if body.HideCompleted {
		t.Fatal("hide_completed must default to false")
	}

	rec = client.do(t, http.MethodPut, "/api/v1/settings/hide-completed", map[string]any{"hide_completed": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("put status = %d, body=%s", rec.Code, rec.Body.String())
	}
	rec = client.do(t, http.MethodGet, "/api/v1/settings/hide-completed", nil)
	jsonUnmarshalRec(t, rec, &body)
	if !body.HideCompleted {
		t.Fatal("hide_completed did not persist")
	}
}

func TestHideCompleted_IsPerUser(t *testing.T) {
	_, client, _, _ := newSearchTestServer(t)
	client.roundTrip(t, http.MethodPost, "/api/v1/users",
		`{"username":"bob","password":"s3cret-s3cret","role":"user"}`)

	rec := client.do(t, http.MethodPut, "/api/v1/settings/hide-completed", map[string]any{"hide_completed": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("admin put status = %d, body=%s", rec.Code, rec.Body.String())
	}

	client.roundTrip(t, http.MethodPost, "/api/v1/auth/logout", "")
	client.roundTrip(t, http.MethodPost, "/api/v1/auth/login",
		`{"username":"bob","password":"s3cret-s3cret"}`)

	var body hideCompletedBody
	rec = client.roundTrip(t, http.MethodGet, "/api/v1/settings/hide-completed", "")
	jsonUnmarshalRec(t, rec, &body)
	if body.HideCompleted {
		t.Error("a second user must not inherit the admin's hide_completed")
	}
}

func TestFileListing_MarksCompletedFiles(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)
	for _, f := range []struct{ id, path string }{{"f1", "done.epub"}, {"f2", "open.epub"}, {"f3", "clip.mp4"}} {
		if err := seedLibraryFile(t, libRoot, f.id, f.path, 10); err != nil {
			t.Fatalf("seed file: %v", err)
		}
	}
	for _, id := range []string{"f1", "f3"} {
		rec := client.do(t, http.MethodPut, "/api/v1/libraries/"+libID+"/files/"+id+"/reading-status",
			map[string]any{"status": "completed"})
		if rec.Code != http.StatusOK {
			t.Fatalf("mark %s completed status = %d, body=%s", id, rec.Code, rec.Body.String())
		}
	}

	rec := client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files?recursive=true", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Files []struct {
			ID        string `json:"id"`
			Completed bool   `json:"completed"`
		} `json:"files"`
	}
	jsonUnmarshalRec(t, rec, &body)
	got := map[string]bool{}
	for _, f := range body.Files {
		got[f.ID] = f.Completed
	}
	want := map[string]bool{"f1": true, "f2": false, "f3": true}
	for id, w := range want {
		if c, ok := got[id]; !ok || c != w {
			t.Errorf("file %s completed = %v (listed %v), want %v", id, c, ok, w)
		}
	}
}
