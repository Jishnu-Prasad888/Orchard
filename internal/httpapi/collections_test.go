package httpapi

import (
	"net/http"
	"testing"
)

func TestCollections_CreateListRename(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)

	rec := client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/collections",
		map[string]string{"name": "Summer reading", "description": "beach books"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Collection collectionResponse `json:"collection"`
	}
	jsonUnmarshalRec(t, rec, &created)
	if created.Collection.Name != "Summer reading" {
		t.Errorf("name = %q", created.Collection.Name)
	}
	if created.Collection.ItemCount != 0 {
		t.Errorf("item_count = %d, want 0", created.Collection.ItemCount)
	}

	rec = client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/collections", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}
	var listed struct {
		Collections []collectionResponse `json:"collections"`
	}
	jsonUnmarshalRec(t, rec, &listed)
	if len(listed.Collections) != 1 {
		t.Fatalf("len = %d, want 1", len(listed.Collections))
	}

	rec = client.do(t, http.MethodPatch, "/api/v1/libraries/"+libID+"/collections/"+created.Collection.ID,
		map[string]string{"name": "Winter reading"})
	if rec.Code != http.StatusOK {
		t.Fatalf("rename status = %d body=%s", rec.Code, rec.Body.String())
	}
	var renamed struct {
		Collection collectionResponse `json:"collection"`
	}
	jsonUnmarshalRec(t, rec, &renamed)
	if renamed.Collection.Name != "Winter reading" {
		t.Errorf("name after rename = %q", renamed.Collection.Name)
	}
}

func TestCollections_CreateDuplicateNameConflict(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)

	client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/collections",
		map[string]string{"name": "Travel"})
	rec := client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/collections",
		map[string]string{"name": "travel"})
	assertErrorEnvelope(t, rec, http.StatusConflict, CodeConflict)
}

func TestCollections_CreateEmptyNameBadRequest(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)
	rec := client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/collections",
		map[string]string{"name": ""})
	assertErrorEnvelope(t, rec, http.StatusBadRequest, CodeBadRequest)
}

func TestCollections_DeleteNotFoundAfterDelete(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)

	rec := client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/collections",
		map[string]string{"name": "Temp"})
	var created struct {
		Collection collectionResponse `json:"collection"`
	}
	jsonUnmarshalRec(t, rec, &created)

	rec = client.do(t, http.MethodDelete, "/api/v1/libraries/"+libID+"/collections/"+created.Collection.ID, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = client.do(t, http.MethodPatch, "/api/v1/libraries/"+libID+"/collections/"+created.Collection.ID,
		map[string]string{"name": "Ghost"})
	assertErrorEnvelope(t, rec, http.StatusNotFound, CodeNotFound)
}

func TestCollections_AddListRemoveFile(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)
	if err := seedLibraryFile(t, libRoot, "f1", "book.epub", 100); err != nil {
		t.Fatal(err)
	}

	rec := client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/collections",
		map[string]string{"name": "Reading list"})
	var created struct {
		Collection collectionResponse `json:"collection"`
	}
	jsonUnmarshalRec(t, rec, &created)
	collID := created.Collection.ID

	rec = client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/collections/"+collID+"/files",
		map[string]string{"file_id": "f1"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("add file status = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/collections/"+collID+"/files", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list files status = %d body=%s", rec.Code, rec.Body.String())
	}
	var files struct {
		Files []fileResponse `json:"files"`
	}
	jsonUnmarshalRec(t, rec, &files)
	if len(files.Files) != 1 || files.Files[0].ID != "f1" {
		t.Fatalf("files = %+v, want [f1]", files.Files)
	}

	rec = client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files/f1/collections", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list file collections status = %d body=%s", rec.Code, rec.Body.String())
	}
	var byFile struct {
		Collections []collectionResponse `json:"collections"`
	}
	jsonUnmarshalRec(t, rec, &byFile)
	if len(byFile.Collections) != 1 || byFile.Collections[0].ID != collID {
		t.Fatalf("collections by file = %+v, want [%s]", byFile.Collections, collID)
	}

	rec = client.do(t, http.MethodDelete, "/api/v1/libraries/"+libID+"/collections/"+collID+"/files/f1", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("remove file status = %d body=%s", rec.Code, rec.Body.String())
	}
	rec = client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/collections/"+collID+"/files", nil)
	jsonUnmarshalRec(t, rec, &files)
	if len(files.Files) != 0 {
		t.Errorf("expected 0 files after remove, got %d", len(files.Files))
	}
}

func TestCollections_ArePerUser(t *testing.T) {
	_, admin, viewer, libID := newAuthzTestServer(t)

	// Find the viewer's user ID to grant it a permission.
	usersResp := admin.do(t, http.MethodGet, "/api/v1/users", nil)
	var users struct {
		Users []struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"users"`
	}
	jsonUnmarshalRec(t, usersResp, &users)
	var viewerID string
	for _, u := range users.Users {
		if u.Username == "viewer" {
			viewerID = u.ID
		}
	}
	if viewerID == "" {
		t.Fatalf("could not find viewer's user ID among %+v", users.Users)
	}

	// Grant the viewer full read/write capability on the library — the
	// point of this test is that collection ownership still isolates it
	// from the admin's even though its *library* access is unrestricted.
	rec := admin.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/permissions", map[string]any{
		"user_id": viewerID,
		"caps":    []string{"read", "create", "edit", "delete"},
		"effect":  "allow",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("grant status = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = admin.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/collections",
		map[string]string{"name": "Admin's list"})
	var created struct {
		Collection collectionResponse `json:"collection"`
	}
	jsonUnmarshalRec(t, rec, &created)

	// The viewer should not see the admin's collection.
	rec = viewer.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/collections", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer list status = %d body=%s", rec.Code, rec.Body.String())
	}
	var listed struct {
		Collections []collectionResponse `json:"collections"`
	}
	jsonUnmarshalRec(t, rec, &listed)
	if len(listed.Collections) != 0 {
		t.Errorf("viewer should see 0 collections, got %d", len(listed.Collections))
	}

	// The viewer cannot rename or delete the admin's collection by ID, even
	// though it holds edit/delete capability on the library itself.
	rec = viewer.do(t, http.MethodPatch, "/api/v1/libraries/"+libID+"/collections/"+created.Collection.ID,
		map[string]string{"name": "Hijacked"})
	assertErrorEnvelope(t, rec, http.StatusNotFound, CodeNotFound)

	rec = viewer.do(t, http.MethodDelete, "/api/v1/libraries/"+libID+"/collections/"+created.Collection.ID, nil)
	assertErrorEnvelope(t, rec, http.StatusNotFound, CodeNotFound)

	// The viewer can create its own collection with the same name the admin used.
	rec = viewer.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/collections",
		map[string]string{"name": "Admin's list"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("viewer's own collection create status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCollections_PinToggle(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)

	rec := client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/collections",
		map[string]string{"name": "Favorites shelf"})
	var created struct {
		Collection collectionResponse `json:"collection"`
	}
	jsonUnmarshalRec(t, rec, &created)
	if created.Collection.Pinned {
		t.Fatal("new collection should not be pinned")
	}

	rec = client.do(t, http.MethodPatch, "/api/v1/libraries/"+libID+"/collections/"+created.Collection.ID,
		map[string]bool{"pinned": true})
	var pinned struct {
		Collection collectionResponse `json:"collection"`
	}
	jsonUnmarshalRec(t, rec, &pinned)
	if !pinned.Collection.Pinned {
		t.Error("expected pinned = true after patch")
	}
}
