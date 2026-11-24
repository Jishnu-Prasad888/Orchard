package httpapi

import (
	"net/http"
	"reflect"
	"testing"
)

func TestHiddenCollectionLibraries_DefaultsEmptyThenRoundTrips(t *testing.T) {
	_, client, _, _ := newSearchTestServer(t)

	var body hiddenLibrariesBody
	rec := client.do(t, http.MethodGet, "/api/v1/settings/hidden-collection-libraries", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, body=%s", rec.Code, rec.Body.String())
	}
	jsonUnmarshalRec(t, rec, &body)
	if len(body.LibraryIDs) != 0 {
		t.Fatalf("default = %v, want empty", body.LibraryIDs)
	}

	rec = client.do(t, http.MethodPut, "/api/v1/settings/hidden-collection-libraries",
		map[string]any{"library_ids": []string{"lib-a", " ", "lib-b", "lib-a"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("put status = %d, body=%s", rec.Code, rec.Body.String())
	}
	rec = client.do(t, http.MethodGet, "/api/v1/settings/hidden-collection-libraries", nil)
	jsonUnmarshalRec(t, rec, &body)
	if want := []string{"lib-a", "lib-b"}; !reflect.DeepEqual(body.LibraryIDs, want) {
		t.Errorf("stored = %v, want %v", body.LibraryIDs, want)
	}
}
