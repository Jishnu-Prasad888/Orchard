package httpapi

import (
	"net/http"
	"testing"
)

// The folder query params accepted by file listing, folder listing, and search
// must be canonicalized by the shared sanitizer before they reach
// authorization keys or SQL. Malformed values (traversal, absolute, targeting
// the metadata directory) are rejected with 400 BAD_REQUEST instead of
// producing surprises downstream.
func TestFolderParamsRejectNonCanonicalPaths(t *testing.T) {
	handler, client, libID, _ := newSearchTestServer(t)
	_ = handler

	bad := []string{
		"../escape",
		"/absolute",
		"a/../../escape",
		".cairn",
		".cairn/photos",
		"a/./b",
	}

	for _, folder := range bad {
		// File listing folder filter.
		rec := client.roundTrip(t, http.MethodGet,
			"/api/v1/libraries/"+libID+"/files?folder="+folder, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("files?folder=%q status = %d, want 400", folder, rec.Code)
		}

		// Folder listing parent filter.
		rec = client.roundTrip(t, http.MethodGet,
			"/api/v1/libraries/"+libID+"/folders?parent="+folder, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("folders?parent=%q status = %d, want 400", folder, rec.Code)
		}

		// Search folder filter.
		rec = client.roundTrip(t, http.MethodGet,
			"/api/v1/libraries/"+libID+"/search?q=summer&folder="+folder, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("search?folder=%q status = %d, want 400", folder, rec.Code)
		}
	}
}

func TestFolderParamsCanonicalized(t *testing.T) {
	handler, client, libID, libRoot := newSearchTestServer(t)

	// Seed one file so listing produces a result for the canonical folder.
	if err := seedLibraryFile(t, libRoot, "f1", "2022/jan.jpg", 100); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_ = handler

	// Leading and trailing separators must collapse to the same canonical
	// folder that matches the seeded path. A leading slash stays rejected as
	// absolute.
	for _, folder := range []string{"2022", "2022/", "2022//"} {
		rec := client.roundTrip(t, http.MethodGet,
			"/api/v1/libraries/"+libID+"/files?folder="+folder, "")
		if rec.Code != http.StatusOK {
			t.Errorf("files?folder=%q status = %d, want 200", folder, rec.Code)
		}
	}
	rec := client.roundTrip(t, http.MethodGet,
		"/api/v1/libraries/"+libID+"/files?folder=/2022", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("files?folder=/2022 status = %d, want 400 (absolute)", rec.Code)
	}
}
