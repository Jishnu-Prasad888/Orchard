package httpapi

import (
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/authz"
	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
)

// seedPhoto writes a small real JPEG under libRoot and indexes it as a photo.
func seedPhoto(t *testing.T, libRoot, id, rel string) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 30, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 30; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 8), uint8(y * 12), 120, 255})
		}
	}
	abs := filepath.Join(libRoot, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(abs)
	if err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(f, img, nil); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	ldb, err := librarydb.Open(filepath.Join(libRoot, ".cairn"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ldb.Close() }()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := ldb.Exec(`INSERT INTO indexed_files
		(id, rel_path, size_bytes, mod_time, content_hash, media_type, status, first_seen_at, last_seen_at, indexed_at)
		VALUES (?, ?, 1, ?, ?, 'photo', 'present', ?, ?, ?)`, id, rel, now, "h-"+id, now, now, now); err != nil {
		t.Fatal(err)
	}
	return abs
}

type docResp struct {
	Memory struct {
		ID       string `json:"id"`
		Revision int    `json:"revision"`
		Body     string `json:"body"`
		Blocks   []struct {
			ID        string  `json:"id"`
			Type      string  `json:"type"`
			Markdown  *string `json:"markdown"`
			Layout    string  `json:"layout"`
			Slideshow *struct {
				Enabled         bool `json:"enabled"`
				IntervalSeconds *int `json:"interval_seconds"`
			} `json:"slideshow"`
			Images []struct {
				ID       string         `json:"id"`
				FileID   string         `json:"file_id"`
				Caption  string         `json:"caption"`
				Crop     map[string]any `json:"crop"`
				Rotation int            `json:"rotation"`
				Edited   bool           `json:"edited"`
				Media    mediaView      `json:"media"`
				Derived  *derivedView   `json:"derived"`
			} `json:"images"`
		} `json:"blocks"`
	} `json:"memory"`
	Warnings []string `json:"warnings"`
}

func decodeDoc(t *testing.T, body []byte) docResp {
	t.Helper()
	var d docResp
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatalf("decode memory: %v (%s)", err, body)
	}
	return d
}

func createMemory(t *testing.T, c *testClient, libID string) docResp {
	t.Helper()
	rec := c.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/memories",
		map[string]string{"title": "My Trip to Kerala", "body": "We left early."})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}
	return decodeDoc(t, rec.Body.Bytes())
}

func TestMemoryDocumentAPI(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)
	seedPhoto(t, libRoot, "p1", "kerala/1.jpg")
	seedPhoto(t, libRoot, "p2", "kerala/2.jpg")
	base := "/api/v1/libraries/" + libID + "/memories/"

	m := createMemory(t, client, libID)
	if len(m.Memory.Blocks) != 1 || m.Memory.Blocks[0].Type != "text" || *m.Memory.Blocks[0].Markdown != "We left early." {
		t.Fatalf("created blocks = %+v", m.Memory.Blocks)
	}
	textID := m.Memory.Blocks[0].ID

	// Notebook flow: + Image with two photos, then + Text after it.
	rec := client.do(t, http.MethodPost, base+m.Memory.ID+"/blocks",
		map[string]any{"type": "image", "file_ids": []string{"p1", "p2"}, "base_revision": m.Memory.Revision})
	if rec.Code != http.StatusOK {
		t.Fatalf("add image block = %d %s", rec.Code, rec.Body.String())
	}
	d := decodeDoc(t, rec.Body.Bytes())
	img := d.Memory.Blocks[1]
	if img.Type != "image" || img.Layout != "grid" || len(img.Images) != 2 {
		t.Fatalf("image block = %+v", img)
	}
	if !img.Images[0].Media.Available || img.Images[0].Media.ThumbnailURL == "" || img.Images[0].Edited {
		t.Errorf("media view = %+v", img.Images[0].Media)
	}
	rec = client.do(t, http.MethodPost, base+m.Memory.ID+"/blocks",
		map[string]any{"type": "text", "markdown": "The next morning…"})
	d = decodeDoc(t, rec.Body.Bytes())
	if len(d.Memory.Blocks) != 3 {
		t.Fatalf("blocks = %d", len(d.Memory.Blocks))
	}

	// Stale revision → 409 with the current revision in details.
	rec = client.do(t, http.MethodPatch, base+m.Memory.ID+"/blocks/"+textID,
		map[string]any{"markdown": "stale", "base_revision": m.Memory.Revision})
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale write = %d", rec.Code)
	}
	var env ErrorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if cur, _ := env.Error.Details["current_revision"].(float64); int(cur) != d.Memory.Revision {
		t.Errorf("conflict details = %+v (want %d)", env.Error.Details, d.Memory.Revision)
	}

	// Caption, crop, then clear the crop with an explicit null.
	imageID := img.Images[0].ID
	rec = client.do(t, http.MethodPatch, base+m.Memory.ID+"/images/"+imageID,
		map[string]any{"caption": "The road to Munnar", "crop": map[string]float64{"x": 0.1, "y": 0, "width": 0.5, "height": 1}})
	d = decodeDoc(t, rec.Body.Bytes())
	got := d.Memory.Blocks[1].Images[0]
	if got.Caption != "The road to Munnar" || got.Crop == nil || !got.Edited {
		t.Fatalf("patched image = %+v", got)
	}
	rec = client.do(t, http.MethodPatch, base+m.Memory.ID+"/images/"+imageID, map[string]any{"crop": nil})
	d = decodeDoc(t, rec.Body.Bytes())
	if got := d.Memory.Blocks[1].Images[0]; got.Crop != nil || got.Edited || got.Caption != "The road to Munnar" {
		t.Fatalf("crop not cleared: %+v", got)
	}

	// Reorder images, change layout and enable a slideshow with an override.
	ids := []string{d.Memory.Blocks[1].Images[1].ID, d.Memory.Blocks[1].Images[0].ID}
	rec = client.do(t, http.MethodPut, base+m.Memory.ID+"/blocks/"+img.ID+"/images/order", map[string]any{"image_ids": ids})
	if rec.Code != http.StatusOK {
		t.Fatalf("reorder images = %d %s", rec.Code, rec.Body.String())
	}
	rec = client.do(t, http.MethodPatch, base+m.Memory.ID+"/blocks/"+img.ID,
		map[string]any{"layout": "featured", "slideshow": map[string]any{"enabled": true, "interval_seconds": 20}})
	d = decodeDoc(t, rec.Body.Bytes())
	blk := d.Memory.Blocks[1]
	if blk.Layout != "featured" || !blk.Slideshow.Enabled || *blk.Slideshow.IntervalSeconds != 20 || blk.Images[0].FileID != "p2" {
		t.Fatalf("block = %+v", blk)
	}
	// interval null → inherit the user setting.
	rec = client.do(t, http.MethodPatch, base+m.Memory.ID+"/blocks/"+img.ID,
		map[string]any{"slideshow": map[string]any{"interval_seconds": nil}})
	d = decodeDoc(t, rec.Body.Bytes())
	if d.Memory.Blocks[1].Slideshow.IntervalSeconds != nil || !d.Memory.Blocks[1].Slideshow.Enabled {
		t.Errorf("interval not inherited: %+v", d.Memory.Blocks[1].Slideshow)
	}

	// Duplicate the image block: same files, new ids.
	rec = client.do(t, http.MethodPost, base+m.Memory.ID+"/blocks/"+img.ID+"/duplicate", nil)
	d = decodeDoc(t, rec.Body.Bytes())
	if len(d.Memory.Blocks) != 4 || d.Memory.Blocks[2].Images[0].FileID != "p2" ||
		d.Memory.Blocks[2].Images[0].ID == d.Memory.Blocks[1].Images[0].ID {
		t.Fatalf("duplicate = %+v", d.Memory.Blocks)
	}

	// Remove from memory never touches the library file.
	rec = client.do(t, http.MethodDelete, base+m.Memory.ID+"/images/"+imageID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove image = %d", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(libRoot, "kerala/1.jpg")); err != nil {
		t.Fatalf("original deleted: %v", err)
	}

	// Move the last block first via the order endpoint.
	d = decodeDoc(t, rec.Body.Bytes())
	order := []string{d.Memory.Blocks[3].ID, d.Memory.Blocks[0].ID, d.Memory.Blocks[1].ID, d.Memory.Blocks[2].ID}
	rec = client.do(t, http.MethodPut, base+m.Memory.ID+"/blocks/order", map[string]any{"block_ids": order})
	d = decodeDoc(t, rec.Body.Bytes())
	if d.Memory.Blocks[0].ID != order[0] {
		t.Errorf("block order = %v", d.Memory.Blocks)
	}
	rec = client.do(t, http.MethodPut, base+m.Memory.ID+"/blocks/order", map[string]any{"block_ids": order[:2]})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("partial order = %d, want 400", rec.Code)
	}

	// The legacy body PUT now refuses to flatten image sections.
	rec = client.do(t, http.MethodPut, base+m.Memory.ID, map[string]string{"title": "x", "body": "flat"})
	if rec.Code != http.StatusConflict {
		t.Errorf("legacy put on structured memory = %d, want 409", rec.Code)
	}

	// Full document PUT round-trip (what the editor autosave sends).
	rec = client.do(t, http.MethodGet, base+m.Memory.ID, nil)
	d = decodeDoc(t, rec.Body.Bytes())
	rec = client.do(t, http.MethodPut, base+m.Memory.ID+"/document", map[string]any{
		"base_revision": d.Memory.Revision,
		"blocks": []map[string]any{
			{"id": "client-block-0001", "type": "text", "markdown": "# Rewritten"},
			{"id": d.Memory.Blocks[2].ID, "type": "image", "layout": "masonry",
				"images": []map[string]any{{"file_id": "p1", "caption": "Tea"}}},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("put document = %d %s", rec.Code, rec.Body.String())
	}
	d = decodeDoc(t, rec.Body.Bytes())
	if len(d.Memory.Blocks) != 2 || d.Memory.Blocks[0].ID != "client-block-0001" || d.Memory.Body != "# Rewritten" {
		t.Fatalf("document = %+v", d.Memory)
	}

	// Captions are searchable through the memory list.
	rec = client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/memories?q=Tea", nil)
	var list memoryListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Memories) != 1 {
		t.Errorf("caption search = %d results", len(list.Memories))
	}
}

func TestMemoryMetadataPatch(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)
	seedPhoto(t, libRoot, "p1", "a.jpg")
	m := createMemory(t, client, libID)
	url := "/api/v1/libraries/" + libID + "/memories/" + m.Memory.ID
	rec := client.do(t, http.MethodPatch, url, map[string]any{
		"title": "Kerala, 2026", "description": "Monsoon trip", "location": "Kochi",
		"memory_date": "2026-09-01T00:00:00Z", "cover_file_id": "p1", "tags": []string{"travel", "family"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch = %d %s", rec.Code, rec.Body.String())
	}
	var resp struct{ Memory memoryResponse }
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	mm := resp.Memory
	if mm.Title != "Kerala, 2026" || mm.Location != "Kochi" || len(mm.Tags) != 2 || mm.Cover == nil || !mm.Cover.Available {
		t.Fatalf("patched = %+v", mm)
	}
	rec = client.do(t, http.MethodPatch, url, map[string]any{"memory_date": nil, "cover_file_id": nil})
	var cleared struct{ Memory memoryResponse }
	_ = json.Unmarshal(rec.Body.Bytes(), &cleared)
	if cleared.Memory.MemoryDate != nil || cleared.Memory.CoverFileID != nil || cleared.Memory.Location != "Kochi" {
		t.Errorf("not cleared: %+v", cleared.Memory)
	}
	rec = client.do(t, http.MethodPatch, url, map[string]any{"title": "   "})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("blank title = %d", rec.Code)
	}
}

func TestMemorySettingsAndDerivedCopies(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)
	orig := seedPhoto(t, libRoot, "p1", "a.jpg")
	before, _ := os.ReadFile(orig)

	rec := client.do(t, http.MethodGet, "/api/v1/settings/memories", nil)
	var s struct {
		Settings struct {
			SlideshowInterval int    `json:"slideshow_interval"`
			EditedCopies      bool   `json:"edited_copies"`
			DefaultLayout     string `json:"default_layout"`
		} `json:"settings"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &s)
	if s.Settings.SlideshowInterval != 15 || s.Settings.EditedCopies || s.Settings.DefaultLayout != "grid" {
		t.Fatalf("defaults = %+v", s.Settings)
	}
	if rec := client.do(t, http.MethodPatch, "/api/v1/settings/memories", map[string]any{"slideshow_interval": 1}); rec.Code != http.StatusBadRequest {
		t.Errorf("interval 1 = %d, want 400", rec.Code)
	}
	rec = client.do(t, http.MethodPatch, "/api/v1/settings/memories",
		map[string]any{"slideshow_interval": 30, "edited_copies": true, "default_layout": "hero"})
	_ = json.Unmarshal(rec.Body.Bytes(), &s)
	if s.Settings.SlideshowInterval != 30 || !s.Settings.EditedCopies || s.Settings.DefaultLayout != "hero" {
		t.Fatalf("updated = %+v", s.Settings)
	}

	m := createMemory(t, client, libID)
	base := "/api/v1/libraries/" + libID + "/memories/" + m.Memory.ID
	rec = client.do(t, http.MethodPost, base+"/blocks", map[string]any{"type": "image", "file_ids": []string{"p1"}})
	d := decodeDoc(t, rec.Body.Bytes())
	if d.Memory.Blocks[1].Layout != "hero" {
		t.Errorf("new block ignored default layout: %q", d.Memory.Blocks[1].Layout)
	}
	if d.Memory.Blocks[1].Images[0].Derived != nil {
		t.Fatal("unedited image got a derived copy")
	}
	imageID := d.Memory.Blocks[1].Images[0].ID
	rec = client.do(t, http.MethodPatch, base+"/images/"+imageID, map[string]any{"rotation": 90, "filter": "warm"})
	d = decodeDoc(t, rec.Body.Bytes())
	der := d.Memory.Blocks[1].Images[0].Derived
	if der == nil || der.Width != 20 || der.Height != 30 {
		t.Fatalf("derived = %+v warnings=%v", der, d.Warnings)
	}
	rec = client.do(t, http.MethodGet, der.URL, nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/jpeg" || rec.Body.Len() == 0 {
		t.Fatalf("serve derived = %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	after, _ := os.ReadFile(orig)
	if string(before) != string(after) {
		t.Fatal("original bytes changed")
	}
	if _, err := os.Stat(filepath.Join(libRoot, ".cairn", "memory-media", m.Memory.ID)); err != nil {
		t.Errorf("derived dir missing: %v", err)
	}

	// Reset → back to the original reference; the copy endpoint 404s.
	rec = client.do(t, http.MethodPatch, base+"/images/"+imageID, map[string]any{"rotation": 0, "filter": "original"})
	d = decodeDoc(t, rec.Body.Bytes())
	if d.Memory.Blocks[1].Images[0].Derived != nil || d.Memory.Blocks[1].Images[0].Edited {
		t.Fatalf("after reset = %+v", d.Memory.Blocks[1].Images[0])
	}
	if rec := client.do(t, http.MethodGet, der.URL, nil); rec.Code != http.StatusNotFound {
		t.Errorf("stale derived = %d, want 404", rec.Code)
	}
}

func TestMemoryImagePermissions(t *testing.T) {
	_, admin, viewer, libID := newAuthzTestServer(t)
	rec := admin.do(t, http.MethodGet, "/api/v1/libraries/"+libID, nil)
	var libResp struct {
		Library struct {
			Root string `json:"root"`
		} `json:"library"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &libResp)
	root := libResp.Library.Root
	if root == "" {
		t.Fatalf("library root unknown: %s", rec.Body.String())
	}
	seedPhoto(t, root, "open1", "shared/open.jpg")
	seedPhoto(t, root, "secret1", "private/secret.jpg")

	vid := viewerID(t, admin)
	grantCap(t, admin, libID, vid, authz.LibraryKey(libID), []string{"read", "create", "edit"})
	denyCap(t, admin, libID, vid, authz.FolderKey(libID, "private"), []string{"read"})

	// The viewer can build a memory from photos they may see…
	m := createMemory(t, viewer, libID)
	base := "/api/v1/libraries/" + libID + "/memories/" + m.Memory.ID
	rec = viewer.do(t, http.MethodPost, base+"/blocks", map[string]any{"type": "image", "file_ids": []string{"open1"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer add permitted photo = %d %s", rec.Code, rec.Body.String())
	}
	// …but not reference one they may not.
	rec = viewer.do(t, http.MethodPost, base+"/blocks", map[string]any{"type": "image", "file_ids": []string{"secret1"}})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer add forbidden photo = %d, want 403", rec.Code)
	}

	// The admin adds the private photo; the viewer sees a placeholder with
	// no name or URLs, while order and caption survive.
	d := decodeDoc(t, viewer.do(t, http.MethodGet, base, nil).Body.Bytes())
	blockID := d.Memory.Blocks[1].ID
	rec = admin.do(t, http.MethodPost, base+"/blocks/"+blockID+"/images", map[string]any{"file_ids": []string{"secret1"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("admin add = %d %s", rec.Code, rec.Body.String())
	}
	d = decodeDoc(t, viewer.do(t, http.MethodGet, base, nil).Body.Bytes())
	hidden := d.Memory.Blocks[1].Images[1]
	if hidden.Media.Status != "forbidden" || hidden.Media.Available || hidden.Media.ThumbnailURL != "" || hidden.Media.Name != "" {
		t.Fatalf("forbidden media leaked: %+v", hidden.Media)
	}
	if !d.Memory.Blocks[1].Images[0].Media.Available {
		t.Errorf("permitted media hidden")
	}
	// Viewer may still edit the memory without touching the hidden reference.
	rec = viewer.do(t, http.MethodPatch, base+"/images/"+d.Memory.Blocks[1].Images[0].ID, map[string]any{"caption": "ok"})
	if rec.Code != http.StatusOK {
		t.Errorf("viewer edit with hidden sibling = %d %s", rec.Code, rec.Body.String())
	}
}
