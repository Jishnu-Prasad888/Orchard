package httpapi

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
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

func TestFileListing_CarriesBookTitlesAndTagNames(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)
	for _, f := range []struct{ id, path string }{{"f1", "dune_v2.epub"}, {"f2", "beach.png"}} {
		if err := seedLibraryFile(t, libRoot, f.id, f.path, 10); err != nil {
			t.Fatalf("seed file: %v", err)
		}
	}
	ldb, err := librarydb.Open(filepath.Join(libRoot, ".orchard"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ldb.Exec(`INSERT INTO book_metadata (file_id, title, extracted_at) VALUES ('f1', 'Dune', '')`); err != nil {
		t.Fatalf("seed metadata: %v", err)
	}
	_ = ldb.Close()

	for _, name := range []string{"sci-fi", "classics"} {
		rec := client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/tags", map[string]any{"name": name})
		var created struct {
			Tag struct {
				ID string `json:"id"`
			} `json:"tag"`
		}
		jsonUnmarshalRec(t, rec, &created)
		rec = client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/files/f1/tags",
			map[string]any{"tag_id": created.Tag.ID})
		if rec.Code/100 != 2 {
			t.Fatalf("tag file status = %d, body=%s", rec.Code, rec.Body.String())
		}
	}

	rec := client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files?recursive=true", nil)
	var body struct {
		Files []struct {
			ID    string   `json:"id"`
			Title string   `json:"title"`
			Tags  []string `json:"tags"`
		} `json:"files"`
	}
	jsonUnmarshalRec(t, rec, &body)
	for _, f := range body.Files {
		switch f.ID {
		case "f1":
			if f.Title != "Dune" {
				t.Errorf("book title = %q, want Dune", f.Title)
			}
			if len(f.Tags) != 2 || f.Tags[0] != "classics" || f.Tags[1] != "sci-fi" {
				t.Errorf("book tags = %v, want [classics sci-fi]", f.Tags)
			}
		case "f2":
			if f.Title != "" || len(f.Tags) != 0 {
				t.Errorf("photo = %+v, want no title and no tags", f)
			}
		}
	}
}
