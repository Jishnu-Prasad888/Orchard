package httpapi

import (
	"net/http"
	"testing"
)

func TestReadingProgress_RoundTripAndClear(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)
	if err := seedLibraryFile(t, libRoot, "f1", "book.epub", 10); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	rec := client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files/f1/progress", nil)
	var body struct {
		Progress *struct {
			Location string  `json:"location"`
			Percent  float64 `json:"percent"`
		} `json:"progress"`
	}
	jsonUnmarshalRec(t, rec, &body)
	if body.Progress != nil {
		t.Fatalf("expected no progress initially, got %+v", body.Progress)
	}

	rec = client.do(t, http.MethodPut, "/api/v1/libraries/"+libID+"/files/f1/progress",
		map[string]any{"location": "epubcfi(/6/4!/4/2)", "percent": 37.5})
	if rec.Code != http.StatusOK {
		t.Fatalf("set progress status = %d, body=%s", rec.Code, rec.Body.String())
	}

	rec = client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files/f1/progress", nil)
	jsonUnmarshalRec(t, rec, &body)
	if body.Progress == nil || body.Progress.Location != "epubcfi(/6/4!/4/2)" || body.Progress.Percent != 37.5 {
		t.Fatalf("progress = %+v, want the saved position", body.Progress)
	}

	rec = client.roundTrip(t, http.MethodDelete, "/api/v1/libraries/"+libID+"/files/f1/progress", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("clear progress status = %d", rec.Code)
	}
	rec = client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files/f1/progress", nil)
	jsonUnmarshalRec(t, rec, &body)
	if body.Progress != nil {
		t.Errorf("expected no progress after clear, got %+v", body.Progress)
	}
}

func TestReadingProgress_RejectsOutOfRangePercent(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)
	if err := seedLibraryFile(t, libRoot, "f1", "book.epub", 10); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	rec := client.do(t, http.MethodPut, "/api/v1/libraries/"+libID+"/files/f1/progress",
		map[string]any{"location": "page:1", "percent": 150})
	assertErrorEnvelope(t, rec, http.StatusBadRequest, CodeBadRequest)
}

func TestReaderPreferences_DefaultsThenRoundTrip(t *testing.T) {
	_, client, _, _ := newSearchTestServer(t)

	rec := client.roundTrip(t, http.MethodGet, "/api/v1/settings/reader", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get defaults status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Preferences ReaderPreferences `json:"preferences"`
	}
	jsonUnmarshalRec(t, rec, &body)
	if body.Preferences != defaultReaderPreferences {
		t.Errorf("defaults = %+v, want %+v", body.Preferences, defaultReaderPreferences)
	}

	want := ReaderPreferences{FontSize: 22, LineSpacing: 1.8, Margin: 40, ReadingWidth: 800, Theme: "sepia"}
	rec = client.do(t, http.MethodPut, "/api/v1/settings/reader", want)
	if rec.Code != http.StatusOK {
		t.Fatalf("set preferences status = %d, body=%s", rec.Code, rec.Body.String())
	}

	rec = client.roundTrip(t, http.MethodGet, "/api/v1/settings/reader", "")
	jsonUnmarshalRec(t, rec, &body)
	if body.Preferences != want {
		t.Errorf("preferences after save = %+v, want %+v", body.Preferences, want)
	}
}

func TestReaderPreferences_RejectsInvalidTheme(t *testing.T) {
	_, client, _, _ := newSearchTestServer(t)
	rec := client.do(t, http.MethodPut, "/api/v1/settings/reader",
		ReaderPreferences{FontSize: 18, LineSpacing: 1.5, Margin: 24, ReadingWidth: 720, Theme: "neon"})
	assertErrorEnvelope(t, rec, http.StatusBadRequest, CodeBadRequest)
}

func TestReaderPreferences_RejectsOutOfRangeFontSize(t *testing.T) {
	_, client, _, _ := newSearchTestServer(t)
	rec := client.do(t, http.MethodPut, "/api/v1/settings/reader",
		ReaderPreferences{FontSize: 200, LineSpacing: 1.5, Margin: 24, ReadingWidth: 720, Theme: "light"})
	assertErrorEnvelope(t, rec, http.StatusBadRequest, CodeBadRequest)
}

func TestReaderPreferences_ArePerUser(t *testing.T) {
	_, client, _, _ := newSearchTestServer(t)
	client.roundTrip(t, http.MethodPost, "/api/v1/users",
		`{"username":"bob","password":"s3cret-s3cret","role":"user"}`)

	want := ReaderPreferences{FontSize: 30, LineSpacing: 2, Margin: 10, ReadingWidth: 600, Theme: "dark"}
	rec := client.do(t, http.MethodPut, "/api/v1/settings/reader", want)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin set preferences status = %d, body=%s", rec.Code, rec.Body.String())
	}

	client.roundTrip(t, http.MethodPost, "/api/v1/auth/logout", "")
	client.roundTrip(t, http.MethodPost, "/api/v1/auth/login",
		`{"username":"bob","password":"s3cret-s3cret"}`)

	rec = client.roundTrip(t, http.MethodGet, "/api/v1/settings/reader", "")
	var body struct {
		Preferences ReaderPreferences `json:"preferences"`
	}
	jsonUnmarshalRec(t, rec, &body)
	if body.Preferences != defaultReaderPreferences {
		t.Errorf("a second user must see the defaults, not the admin's preferences, got %+v", body.Preferences)
	}
}
