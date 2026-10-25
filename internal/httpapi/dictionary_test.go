package httpapi

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/Jishnu-Prasad888/Orchard/internal/audit"
	"github.com/Jishnu-Prasad888/Orchard/internal/auth"
	"github.com/Jishnu-Prasad888/Orchard/internal/crypto"
	"github.com/Jishnu-Prasad888/Orchard/internal/db"
	"github.com/Jishnu-Prasad888/Orchard/internal/dictionary"
	"github.com/Jishnu-Prasad888/Orchard/internal/library"
)

// installFixtureWordNet writes a one-word WordNet ("house") straight into
// dir, as an install would have left it.
func installFixtureWordNet(t *testing.T, dir string) {
	t.Helper()
	wn := filepath.Join(dir, dictionary.WordNetID)
	if err := os.MkdirAll(wn, 0o755); err != nil {
		t.Fatal(err)
	}
	data := "  1 header  \n"
	off := len(data)
	data += fmt.Sprintf("%08d 00 n 01 house 0 000 | a dwelling that serves as living quarters  \n", off)
	files := map[string]string{
		"data.noun":  data,
		"index.noun": fmt.Sprintf("  1 header  \nhouse n 1 0 1 0 %08d  \n", off),
		"manifest.json": `{"id":"wordnet","kind":"wordnet","name":"WordNet","version":"2024",
			"installed_at":"2026-01-01T00:00:00Z","enabled":true}`,
	}
	for _, pos := range []string{"noun", "verb", "adj", "adv"} {
		files[pos+".exc"] = ""
		if pos != "noun" {
			files["data."+pos] = ""
			files["index."+pos] = ""
		}
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(wn, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func newDictionaryTestServer(t *testing.T) (*testClient, string, string) {
	t.Helper()
	tmpDir := t.TempDir()
	pool, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if err := db.Migrate(pool); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditSvc := audit.New(pool, logger)
	libManager := library.NewManager(pool, logger, auditSvc, crypto.NewKeys(""))
	libRoot := filepath.Join(tmpDir, "library")
	if err := os.MkdirAll(libRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	lib, _, err := libManager.Register(context.Background(), libRoot, "Test Library")
	if err != nil {
		t.Fatal(err)
	}

	dictDir := filepath.Join(tmpDir, "dictionaries")
	installFixtureWordNet(t, dictDir)
	dicts, err := dictionary.NewManager(dictDir, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dicts.Close)

	handler := New(Dependencies{
		Logger:       logger,
		DB:           pool,
		Auth:         auth.NewService(pool, logger, auditSvc),
		Libraries:    libManager,
		Dictionaries: dicts,
	}).Handler()
	client := &testClient{handler: handler}
	client.roundTrip(t, http.MethodPost, "/api/v1/auth/bootstrap",
		`{"username":"admin","password":"correct-horse-battery"}`)
	client.roundTrip(t, http.MethodPost, "/api/v1/auth/login",
		`{"username":"admin","password":"correct-horse-battery"}`)
	return client, lib.ID, libRoot
}

type lookupBody struct {
	Word    string `json:"word"`
	Results []struct {
		DictionaryID string `json:"dictionary_id"`
		Entries      []struct {
			Headword string `json:"headword"`
			Senses   []struct {
				Definition string `json:"definition"`
			} `json:"senses"`
		} `json:"entries"`
	} `json:"results"`
}

func TestDictionaryLookupAndToggle(t *testing.T) {
	client, _, _ := newDictionaryTestServer(t)

	var list struct {
		Dictionaries []struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		} `json:"dictionaries"`
		Available bool `json:"available"`
	}
	jsonUnmarshalRec(t, client.do(t, http.MethodGet, "/api/v1/dictionaries", nil), &list)
	if !list.Available || len(list.Dictionaries) != 1 || list.Dictionaries[0].ID != "wordnet" {
		t.Fatalf("list = %+v", list)
	}

	var got lookupBody
	jsonUnmarshalRec(t, client.do(t, http.MethodGet, "/api/v1/dictionaries/lookup?word=Houses.", nil), &got)
	if got.Word != "Houses" || len(got.Results) != 1 || got.Results[0].Entries[0].Headword != "house" ||
		got.Results[0].Entries[0].Senses[0].Definition != "a dwelling that serves as living quarters" {
		t.Fatalf("lookup = %+v", got)
	}

	rec := client.do(t, http.MethodGet, "/api/v1/dictionaries/lookup?word=", nil)
	assertErrorEnvelope(t, rec, http.StatusBadRequest, CodeBadRequest)

	rec = client.do(t, http.MethodPatch, "/api/v1/dictionaries/wordnet", map[string]any{"enabled": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("disable status = %d, body=%s", rec.Code, rec.Body.String())
	}
	jsonUnmarshalRec(t, client.do(t, http.MethodGet, "/api/v1/dictionaries", nil), &list)
	if list.Available {
		t.Error("still available with the only dictionary off")
	}
	got = lookupBody{}
	jsonUnmarshalRec(t, client.do(t, http.MethodGet, "/api/v1/dictionaries/lookup?word=house", nil), &got)
	if len(got.Results) != 0 {
		t.Errorf("lookup with dictionary off = %+v", got)
	}

	rec = client.do(t, http.MethodPost, "/api/v1/dictionaries/install", map[string]any{"id": "wordnet"})
	assertErrorEnvelope(t, rec, http.StatusConflict, CodeConflict)
	rec = client.do(t, http.MethodPost, "/api/v1/dictionaries/install", map[string]any{"id": "klingon"})
	assertErrorEnvelope(t, rec, http.StatusNotFound, CodeNotFound)
	rec = client.do(t, http.MethodPatch, "/api/v1/dictionaries/nope", map[string]any{"enabled": true})
	assertErrorEnvelope(t, rec, http.StatusNotFound, CodeNotFound)
	rec = client.roundTrip(t, http.MethodDelete, "/api/v1/dictionaries/install", "")
	assertErrorEnvelope(t, rec, http.StatusConflict, CodeConflict)

	rec = client.roundTrip(t, http.MethodDelete, "/api/v1/dictionaries/wordnet", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("remove status = %d", rec.Code)
	}
	jsonUnmarshalRec(t, client.do(t, http.MethodGet, "/api/v1/dictionaries", nil), &list)
	if len(list.Dictionaries) != 0 {
		t.Errorf("after remove = %+v", list)
	}
}

func TestDictionaryManagementRequiresAdmin(t *testing.T) {
	client, _, _ := newDictionaryTestServer(t)
	client.roundTrip(t, http.MethodPost, "/api/v1/users",
		`{"username":"bob","password":"s3cret-s3cret","role":"user"}`)
	client.roundTrip(t, http.MethodPost, "/api/v1/auth/logout", "")
	client.roundTrip(t, http.MethodPost, "/api/v1/auth/login", `{"username":"bob","password":"s3cret-s3cret"}`)

	// Members read and look up...
	if rec := client.do(t, http.MethodGet, "/api/v1/dictionaries", nil); rec.Code != http.StatusOK {
		t.Errorf("list as member = %d", rec.Code)
	}
	if rec := client.do(t, http.MethodGet, "/api/v1/dictionaries/lookup?word=house", nil); rec.Code != http.StatusOK {
		t.Errorf("lookup as member = %d", rec.Code)
	}
	// ...but don't manage.
	for _, req := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/v1/dictionaries/catalog", nil},
		{http.MethodPost, "/api/v1/dictionaries/install", map[string]any{"id": "wordnet"}},
		{http.MethodDelete, "/api/v1/dictionaries/install", nil},
		{http.MethodPatch, "/api/v1/dictionaries/wordnet", map[string]any{"enabled": false}},
		{http.MethodDelete, "/api/v1/dictionaries/wordnet", nil},
	} {
		assertErrorEnvelope(t, client.do(t, req.method, req.path, req.body), http.StatusForbidden, CodeForbidden)
	}
}

func TestSavedWordsRoundTrip(t *testing.T) {
	client, libID, libRoot := newDictionaryTestServer(t)
	if err := seedLibraryFile(t, libRoot, "f1", "book.epub", 10); err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/libraries/" + libID

	rec := client.do(t, http.MethodPost, base+"/files/f1/words", map[string]any{
		"word": "house", "text": "houses", "location": "page:2",
		"definition": "noun · a dwelling", "source": "WordNet",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Word struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		} `json:"word"`
	}
	jsonUnmarshalRec(t, rec, &created)
	if created.Word.ID == "" || created.Word.Text != "houses" {
		t.Fatalf("created = %+v", created)
	}

	rec = client.do(t, http.MethodPost, base+"/files/f1/words", map[string]any{"word": "", "location": "page:2"})
	assertErrorEnvelope(t, rec, http.StatusBadRequest, CodeBadRequest)

	var list struct {
		Words []struct {
			Word       string `json:"word"`
			Definition string `json:"definition"`
			Source     string `json:"source"`
		} `json:"words"`
	}
	jsonUnmarshalRec(t, client.do(t, http.MethodGet, base+"/files/f1/words", nil), &list)
	if len(list.Words) != 1 || list.Words[0].Definition != "noun · a dwelling" || list.Words[0].Source != "WordNet" {
		t.Fatalf("list = %+v", list)
	}

	if rec := client.roundTrip(t, http.MethodDelete, base+"/words/"+created.Word.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", rec.Code)
	}
	rec = client.roundTrip(t, http.MethodDelete, base+"/words/"+created.Word.ID, "")
	assertErrorEnvelope(t, rec, http.StatusNotFound, CodeNotFound)
}
