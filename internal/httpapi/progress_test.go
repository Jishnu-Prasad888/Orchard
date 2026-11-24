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

func TestListInProgress_ExcludesUnstartedAndFinished(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)
	if err := seedLibraryFile(t, libRoot, "f1", "unstarted.epub", 10); err != nil {
		t.Fatalf("seed f1: %v", err)
	}
	if err := seedLibraryFile(t, libRoot, "f2", "reading.epub", 10); err != nil {
		t.Fatalf("seed f2: %v", err)
	}
	if err := seedLibraryFile(t, libRoot, "f3", "finished.epub", 10); err != nil {
		t.Fatalf("seed f3: %v", err)
	}
	client.do(t, http.MethodPut, "/api/v1/libraries/"+libID+"/files/f2/progress",
		map[string]any{"location": "page:5", "percent": 40})
	client.do(t, http.MethodPut, "/api/v1/libraries/"+libID+"/files/f3/progress",
		map[string]any{"location": "page:10", "percent": 100})

	rec := client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/reading/in-progress", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Files []struct {
			ID      string  `json:"id"`
			Percent float64 `json:"percent"`
		} `json:"files"`
	}
	jsonUnmarshalRec(t, rec, &body)
	if len(body.Files) != 1 || body.Files[0].ID != "f2" {
		t.Fatalf("files = %+v, want only f2", body.Files)
	}
	if body.Files[0].Percent != 40 {
		t.Errorf("percent = %v, want 40", body.Files[0].Percent)
	}
}

func TestListInProgress_RespectsLimitQueryParam(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)
	if err := seedLibraryFile(t, libRoot, "f1", "a.epub", 10); err != nil {
		t.Fatalf("seed f1: %v", err)
	}
	if err := seedLibraryFile(t, libRoot, "f2", "b.epub", 10); err != nil {
		t.Fatalf("seed f2: %v", err)
	}
	client.do(t, http.MethodPut, "/api/v1/libraries/"+libID+"/files/f1/progress",
		map[string]any{"location": "page:1", "percent": 10})
	client.do(t, http.MethodPut, "/api/v1/libraries/"+libID+"/files/f2/progress",
		map[string]any{"location": "page:1", "percent": 20})

	rec := client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/reading/in-progress?limit=1", nil)
	var body struct {
		Files []struct {
			ID string `json:"id"`
		} `json:"files"`
	}
	jsonUnmarshalRec(t, rec, &body)
	if len(body.Files) != 1 {
		t.Fatalf("files = %+v, want exactly 1", body.Files)
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

	want := ReaderPreferences{
		FontSize: 22, LineSpacing: 1.8, Margin: 40, ReadingWidth: 800, Theme: "sepia",
		ReadingDirection: "vertical", PagesPerView: 2,
	}
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

func TestReaderPreferences_DefaultThemeFollowsTheApp(t *testing.T) {
	if defaultReaderPreferences.Theme != "auto" {
		t.Errorf("default theme = %q, want auto", defaultReaderPreferences.Theme)
	}
	_, client, _, _ := newSearchTestServer(t)
	rec := client.do(t, http.MethodPut, "/api/v1/settings/reader",
		ReaderPreferences{FontSize: 18, LineSpacing: 1.5, Margin: 24, ReadingWidth: 720, Theme: "auto"})
	if rec.Code != http.StatusOK {
		t.Fatalf("set auto theme status = %d, body=%s", rec.Code, rec.Body.String())
	}
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

// TestReadingProgress_FinishingMarksCompleted: reading to 100% ticks
// Completed, as the menu's checkbox would; 99% does not.
func TestReadingProgress_FinishingMarksCompleted(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)
	if err := seedLibraryFile(t, libRoot, "f1", "book.epub", 10); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	status := func() string {
		rec := client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files/f1/reading-status", nil)
		var body struct {
			Status *string `json:"status"`
		}
		jsonUnmarshalRec(t, rec, &body)
		if body.Status == nil {
			return ""
		}
		return *body.Status
	}

	client.do(t, http.MethodPut, "/api/v1/libraries/"+libID+"/files/f1/progress",
		map[string]any{"location": "page:99", "percent": 99})
	if got := status(); got != "" {
		t.Fatalf("status at 99%% = %q, want none", got)
	}
	rec := client.do(t, http.MethodPut, "/api/v1/libraries/"+libID+"/files/f1/progress",
		map[string]any{"location": "page:100", "percent": 100})
	var body struct {
		Completed bool `json:"completed"`
	}
	jsonUnmarshalRec(t, rec, &body)
	if !body.Completed {
		t.Errorf("response completed = false, want true")
	}
	if got := status(); got != "completed" {
		t.Errorf("status at 100%% = %q, want completed", got)
	}
}

// TestForgetFileReading clears the open, the resume point and Completed.
func TestForgetFileReading(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)
	if err := seedLibraryFile(t, libRoot, "f1", "book.epub", 10); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	base := "/api/v1/libraries/" + libID + "/files/"
	client.roundTrip(t, http.MethodPost, base+"f1/opened", "")
	client.do(t, http.MethodPut, base+"f1/progress", map[string]any{"location": "page:100", "percent": 100})

	rec := client.roundTrip(t, http.MethodPost, base+"f1/forget", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("forget status = %d, body=%s", rec.Code, rec.Body.String())
	}

	rec = client.do(t, http.MethodGet, base+"f1/progress", nil)
	var progress struct {
		Progress any `json:"progress"`
	}
	jsonUnmarshalRec(t, rec, &progress)
	if progress.Progress != nil {
		t.Errorf("progress after forget = %v, want none", progress.Progress)
	}
	rec = client.do(t, http.MethodGet, base+"recently-opened", nil)
	var opened struct {
		Files []struct {
			ID string `json:"id"`
		} `json:"files"`
	}
	jsonUnmarshalRec(t, rec, &opened)
	if len(opened.Files) != 0 {
		t.Errorf("recently opened after forget = %v, want empty", opened.Files)
	}
	rec = client.do(t, http.MethodGet, base+"f1/reading-status", nil)
	var status struct {
		Status *string `json:"status"`
	}
	jsonUnmarshalRec(t, rec, &status)
	if status.Status != nil {
		t.Errorf("status after forget = %q, want none", *status.Status)
	}
}
