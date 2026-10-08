package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
	"github.com/Jishnu-Prasad888/Cairn/internal/authz"
	"github.com/Jishnu-Prasad888/Cairn/internal/fts"
	"github.com/Jishnu-Prasad888/Cairn/internal/library"
	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
	"github.com/Jishnu-Prasad888/Cairn/internal/markdown"
	"github.com/Jishnu-Prasad888/Cairn/internal/memories"
	"github.com/Jishnu-Prasad888/Cairn/internal/usersettings"
)

// apiBase prefixes URLs handed to clients.
const apiBase = "/api/v1"

// openMemoryStore opens the per-library DB and returns a MemoryStore.
func (s *Server) openMemoryStore(
	w http.ResponseWriter, r *http.Request, lib *library.Library,
) (*memories.MemoryStore, func(), bool) {
	if lib.Status == library.StatusOffline {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusConflict,
			CodeConflict, "Library is offline.")
		return nil, nil, false
	}
	cairnDir := lib.Root + "/.cairn"
	db, err := librarydb.Open(cairnDir)
	if err != nil {
		s.logger.Error("open library db for memories", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to open library database.")
		return nil, nil, false
	}
	return memories.NewMemoryStore(db), func() { _ = db.Close() }, true
}

func (s *Server) deriverFor(lib *library.Library) *memories.Deriver {
	return &memories.Deriver{LibraryRoot: lib.Root, CairnDir: lib.Root + "/.cairn", Keys: s.keys}
}

// can is requireCap without the 403 side effect, for decisions that shape a
// response (e.g. hiding a referenced photo the viewer may not see).
func (s *Server) can(ctx context.Context, u *auth.User, key string, caps ...authz.Capability) bool {
	if s.authz == nil {
		return u.Role == auth.RoleAdmin
	}
	ok, err := s.authz.Can(ctx, principalFrom(u), key, caps...)
	if err != nil {
		s.logger.Error("authorization check", "key", key, "error", err)
		return false
	}
	return ok
}

// memorySettingsFor returns the caller's memory preferences, or the defaults
// when the server database is not wired (tests, minimal deployments).
func (s *Server) memorySettingsFor(ctx context.Context, u *auth.User) usersettings.MemorySettings {
	if s.db == nil {
		return usersettings.DefaultMemorySettings()
	}
	settings, err := usersettings.NewStore(s.db).Memory(ctx, u.ID)
	if err != nil {
		s.logger.Error("read memory settings", "user_id", u.ID, "error", err)
		return usersettings.DefaultMemorySettings()
	}
	return settings
}

/* ------------------------------ responses ------------------------------ */

// memoryResponse is the API representation of a memory. Blocks is present
// on single-memory responses and omitted from lists.
type memoryResponse struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Body        string          `json:"body"`
	MemoryDate  *string         `json:"memory_date,omitempty"`
	Description string          `json:"description"`
	Location    string          `json:"location"`
	CoverFileID *string         `json:"cover_file_id,omitempty"`
	Cover       *mediaView      `json:"cover,omitempty"`
	Tags        []string        `json:"tags"`
	Revision    int             `json:"revision"`
	Deleted     bool            `json:"deleted"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
	Blocks      []blockResponse `json:"blocks,omitempty"`
}

type slideshowJSON struct {
	Enabled bool `json:"enabled"`
	// IntervalSeconds overrides the viewer's slideshow setting; null inherits.
	IntervalSeconds *int `json:"interval_seconds"`
}

type blockResponse struct {
	ID        string                `json:"id"`
	Type      string                `json:"type"`
	Position  int                   `json:"position"`
	Markdown  *string               `json:"markdown,omitempty"`
	Layout    string                `json:"layout,omitempty"`
	Slideshow *slideshowJSON        `json:"slideshow,omitempty"`
	Images    []memoryImageResponse `json:"images,omitempty"`
}

type memoryImageResponse struct {
	ID          string               `json:"id"`
	Position    int                  `json:"position"`
	FileID      string               `json:"file_id"`
	Caption     string               `json:"caption"`
	Crop        *memories.Crop       `json:"crop"`
	Rotation    int                  `json:"rotation"`
	Filter      string               `json:"filter"`
	Adjustments memories.Adjustments `json:"adjustments"`
	// Edited is true when any visual edit is set; false means the original
	// is shown as-is and no derived copy exists.
	Edited  bool         `json:"edited"`
	Media   mediaView    `json:"media"`
	Derived *derivedView `json:"derived"`
}

// mediaView is what a viewer may know about a referenced library file.
// Status is present, missing, deleted, forbidden or unknown; only a present,
// permitted file carries URLs. Captions, order and layout are kept regardless,
// so an unavailable image still holds its place in the memory.
type mediaView struct {
	Available    bool   `json:"available"`
	Status       string `json:"status"`
	Name         string `json:"name,omitempty"`
	MediaType    string `json:"media_type,omitempty"`
	MIMEType     string `json:"mime_type,omitempty"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
	OriginalURL  string `json:"original_url,omitempty"`
}

type derivedView struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

func baseMemoryResponse(m *memories.Memory) memoryResponse {
	resp := memoryResponse{
		ID:          m.ID,
		Title:       m.Title,
		Body:        m.Body,
		Description: m.Description,
		Location:    m.Location,
		Tags:        m.Tags,
		Revision:    m.Revision,
		Deleted:     m.Deleted,
		CreatedAt:   m.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   m.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if resp.Tags == nil {
		resp.Tags = []string{}
	}
	if m.MemoryDate != nil {
		v := m.MemoryDate.UTC().Format(time.RFC3339)
		resp.MemoryDate = &v
	}
	if m.CoverFileID != "" {
		v := m.CoverFileID
		resp.CoverFileID = &v
	}
	return resp
}

// mediaResolver turns file IDs into permission-checked media views.
type mediaResolver struct {
	s       *Server
	ctx     context.Context
	u       *auth.User
	lib     *library.Library
	sources map[string]*memories.Source
	cache   map[string]mediaView
}

func (s *Server) newMediaResolver(ctx context.Context, u *auth.User, lib *library.Library,
	store *memories.MemoryStore, ids []string) (*mediaResolver, error) {
	sources, err := store.LookupSources(ctx, ids)
	if err != nil {
		return nil, err
	}
	return &mediaResolver{s: s, ctx: ctx, u: u, lib: lib, sources: sources, cache: map[string]mediaView{}}, nil
}

func (mr *mediaResolver) view(fileID string) mediaView {
	if v, ok := mr.cache[fileID]; ok {
		return v
	}
	src := mr.sources[fileID]
	var v mediaView
	switch {
	case src == nil:
		v = mediaView{Status: "unknown"}
	case !mr.s.can(mr.ctx, mr.u, authz.FileKey(mr.lib.ID, src.RelPath), authz.CapRead):
		v = mediaView{Status: "forbidden"}
	default:
		v = mediaView{
			Status:    src.Status,
			Available: src.Available(),
			Name:      baseName(src.RelPath),
			MediaType: src.MediaType,
			MIMEType:  src.MIMEType,
			Width:     src.Width,
			Height:    src.Height,
		}
		if v.Available {
			base := apiBase + "/libraries/" + mr.lib.ID + "/files/" + fileID
			v.ThumbnailURL = base + "/thumbnail"
			if mr.s.can(mr.ctx, mr.u, authz.FileKey(mr.lib.ID, src.RelPath), authz.CapDownload) {
				v.OriginalURL = base + "/download"
			}
		}
	}
	mr.cache[fileID] = v
	return v
}

func baseName(rel string) string {
	for i := len(rel) - 1; i >= 0; i-- {
		if rel[i] == '/' {
			return rel[i+1:]
		}
	}
	return rel
}

// documentResponse renders a memory with blocks, media views and derived
// copies for the requesting user.
func (s *Server) documentResponse(ctx context.Context, u *auth.User, lib *library.Library,
	store *memories.MemoryStore, m *memories.Memory, blocks []*memories.Block) (memoryResponse, error) {
	resp := baseMemoryResponse(m)
	ids := []string{m.CoverFileID}
	for _, b := range blocks {
		for _, img := range b.Images {
			ids = append(ids, img.SourceFileID)
		}
	}
	mr, err := s.newMediaResolver(ctx, u, lib, store, ids)
	if err != nil {
		return resp, err
	}
	derived, err := store.ListDerived(ctx, m.ID)
	if err != nil {
		return resp, err
	}
	if m.CoverFileID != "" {
		v := mr.view(m.CoverFileID)
		resp.Cover = &v
	}
	resp.Blocks = make([]blockResponse, 0, len(blocks))
	for _, b := range blocks {
		br := blockResponse{ID: b.ID, Type: string(b.Type), Position: b.Position}
		if b.Type == memories.BlockText {
			md := b.Markdown
			br.Markdown = &md
		} else {
			br.Layout = string(b.Layout)
			br.Slideshow = &slideshowJSON{Enabled: b.Slideshow, IntervalSeconds: b.SlideshowInterval}
			br.Images = make([]memoryImageResponse, 0, len(b.Images))
			for _, img := range b.Images {
				ir := memoryImageResponse{
					ID:          img.ID,
					Position:    img.Position,
					FileID:      img.SourceFileID,
					Caption:     img.Caption,
					Crop:        img.Edits.Crop,
					Rotation:    img.Edits.Rotation,
					Filter:      img.Edits.Filter,
					Adjustments: img.Edits.Adjustments,
					Edited:      !img.Edits.IsDefault(),
					Media:       mr.view(img.SourceFileID),
				}
				if ir.Filter == "" {
					ir.Filter = memories.FilterOriginal
				}
				if d, ok := derived[img.ID]; ok && ir.Media.Available {
					ir.Derived = &derivedView{
						ID:     d.ID,
						URL:    apiBase + "/libraries/" + lib.ID + "/memories/" + m.ID + "/images/" + img.ID + "/derived?v=" + d.ID,
						Width:  d.Width,
						Height: d.Height,
					}
				}
				br.Images = append(br.Images, ir)
			}
		}
		resp.Blocks = append(resp.Blocks, br)
	}
	return resp, nil
}

// memoryRefResponse is a single parsed internal reference, returned to clients
// so the UI can render links and bidirectional relations without reparsing.
type memoryRefResponse struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

func toMemoryRefResponses(refs []markdown.Reference) []memoryRefResponse {
	out := make([]memoryRefResponse, 0, len(refs))
	for _, r := range refs {
		out = append(out, memoryRefResponse{Type: string(r.Type), ID: r.ID})
	}
	return out
}

// memoryListResponse wraps a page of memories with its next cursor.
type memoryListResponse struct {
	Memories []memoryResponse `json:"memories"`
	Next     string           `json:"next_cursor,omitempty"`
}

/* ------------------------------- inputs -------------------------------- */

// optional distinguishes an absent JSON field from an explicit null.
type optional[T any] struct {
	Set   bool
	Null  bool
	Value T
}

func (o *optional[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(bytes.TrimSpace(b)) == "null" {
		o.Null = true
		return nil
	}
	return json.Unmarshal(b, &o.Value)
}

type imageInput struct {
	ID          string                `json:"id"`
	FileID      string                `json:"file_id"`
	Caption     string                `json:"caption"`
	Crop        *memories.Crop        `json:"crop"`
	Rotation    int                   `json:"rotation"`
	Filter      string                `json:"filter"`
	Adjustments *memories.Adjustments `json:"adjustments"`
}

func (in imageInput) toImage() *memories.Image {
	img := &memories.Image{ID: in.ID, SourceFileID: in.FileID, Caption: in.Caption,
		Edits: memories.Edits{Crop: in.Crop, Rotation: in.Rotation, Filter: in.Filter}}
	if in.Adjustments != nil {
		img.Edits.Adjustments = *in.Adjustments
	}
	return img
}

type blockInput struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Markdown  string         `json:"markdown"`
	Layout    string         `json:"layout"`
	Slideshow *slideshowJSON `json:"slideshow"`
	Images    []imageInput   `json:"images"`
}

func (in blockInput) toBlock() *memories.Block {
	b := &memories.Block{ID: in.ID, Type: memories.BlockType(in.Type), Markdown: in.Markdown,
		Layout: memories.Layout(in.Layout)}
	if in.Slideshow != nil {
		b.Slideshow = in.Slideshow.Enabled
		b.SlideshowInterval = in.Slideshow.IntervalSeconds
	}
	if in.Images != nil || b.Type == memories.BlockImage {
		b.Images = make([]*memories.Image, 0, len(in.Images))
		for _, img := range in.Images {
			b.Images = append(b.Images, img.toImage())
		}
	}
	return b
}

/* ---------------------------- shared plumbing --------------------------- */

// memoryCtx bundles what every memory handler resolves first.
type memoryCtx struct {
	lib     *library.Library
	store   *memories.MemoryStore
	cleanup func()
}

// openMemory resolves the library, checks caps on the memory (or the library
// when memoryID is empty) and opens the store.
func (s *Server) openMemory(w http.ResponseWriter, r *http.Request, u *auth.User, memoryID string,
	caps ...authz.Capability) (*memoryCtx, bool) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return nil, false
	}
	key := authz.LibraryKey(lib.ID)
	if memoryID != "" {
		key = authz.EntityKey("m", lib.ID, memoryID)
	}
	if !s.requireCap(w, r, u, key, caps...) {
		return nil, false
	}
	store, cleanup, ok := s.openMemoryStore(w, r, lib)
	if !ok {
		return nil, false
	}
	return &memoryCtx{lib: lib, store: store, cleanup: cleanup}, true
}

// checkNewSources enforces read permission on every library file a write
// newly references (files already in the memory were checked when added).
func (s *Server) checkNewSources(w http.ResponseWriter, r *http.Request, u *auth.User, mc *memoryCtx,
	before, after []*memories.Block) bool {
	had := map[string]struct{}{}
	for _, b := range before {
		for _, img := range b.Images {
			had[img.SourceFileID] = struct{}{}
		}
	}
	var fresh []string
	for _, b := range after {
		for _, img := range b.Images {
			if _, ok := had[img.SourceFileID]; !ok {
				fresh = append(fresh, img.SourceFileID)
			}
		}
	}
	if len(fresh) == 0 {
		return true
	}
	sources, err := mc.store.LookupSources(r.Context(), fresh)
	if err != nil {
		s.writeMemoryError(w, r, err)
		return false
	}
	for _, id := range fresh {
		src, ok := sources[id]
		if !ok {
			continue // the store rejects unknown media with a 400
		}
		if !s.requireCap(w, r, u, authz.FileKey(mc.lib.ID, src.RelPath), authz.CapRead) {
			return false
		}
	}
	return true
}

// saveAndRespond persists blocks, syncs edited copies per the user's
// setting, and writes the enriched memory.
func (s *Server) saveAndRespond(w http.ResponseWriter, r *http.Request, u *auth.User, mc *memoryCtx,
	memoryID string, base *int, before *memories.Memory, blocks []*memories.Block, status int) {
	if !s.checkNewSources(w, r, u, mc, before.Blocks, blocks) {
		return
	}
	m, err := mc.store.SaveDocument(r.Context(), memoryID, base, blocks)
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	s.respondDocument(w, r, u, mc, m, status, needsDerivedSync(before.Blocks, m.Blocks))
}

// needsDerivedSync reports whether a save could have created, staled or
// orphaned an edited copy — so text-only autosaves skip the pass.
func needsDerivedSync(before, after []*memories.Block) bool {
	sig := func(blocks []*memories.Block) map[string]string {
		out := map[string]string{}
		for _, b := range blocks {
			for _, img := range b.Images {
				out[img.ID] = img.Edits.Signature(img.SourceFileID) + "|" + img.DerivedID
			}
		}
		return out
	}
	a, b := sig(before), sig(after)
	if len(a) != len(b) {
		return true
	}
	for k, v := range a {
		if b[k] != v {
			return true
		}
	}
	for _, blk := range after {
		for _, img := range blk.Images {
			if !img.Edits.IsDefault() && img.DerivedID == "" {
				return true
			}
		}
	}
	return false
}

// respondDocument optionally runs the derived-copy pass and writes the
// enriched document. A derived failure never fails the save: the client can
// always render edits itself; warnings explain what happened.
func (s *Server) respondDocument(w http.ResponseWriter, r *http.Request, u *auth.User, mc *memoryCtx,
	m *memories.Memory, status int, sync bool) {
	var warnings []string
	if sync {
		settings := s.memorySettingsFor(r.Context(), u)
		res, err := mc.store.SyncDerived(r.Context(), m.ID, s.deriverFor(mc.lib), settings.EditedCopies)
		switch {
		case errors.Is(err, memories.ErrReservedPathConflict):
			s.logger.Warn("memory-media path conflict", "library_id", mc.lib.ID)
			warnings = append(warnings, "Edited copies were not created: .cairn/memory-media is occupied by something Cairn did not create. Nothing there was changed.")
		case err != nil:
			s.logger.Error("sync memory derived media", "memory_id", m.ID, "error", err)
			warnings = append(warnings, "Edited copies could not be updated.")
		case res.Failed > 0:
			warnings = append(warnings, strconv.Itoa(res.Failed)+" edited copies could not be rendered.")
		}
		if fresh, err := mc.store.GetDocument(r.Context(), m.ID); err == nil {
			m = fresh
		}
	}
	resp, err := s.documentResponse(r.Context(), u, mc.lib, mc.store, m, m.Blocks)
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	body := map[string]any{"memory": resp}
	if len(warnings) > 0 {
		body["warnings"] = warnings
	}
	writeJSON(w, s.logger, status, body)
}

// loadForMutation reads the current document for a granular edit.
func (s *Server) loadForMutation(w http.ResponseWriter, r *http.Request, mc *memoryCtx, memoryID string) (*memories.Memory, bool) {
	m, err := mc.store.GetDocument(r.Context(), memoryID)
	if err != nil {
		s.writeMemoryError(w, r, err)
		return nil, false
	}
	if m.Deleted {
		s.writeMemoryError(w, r, memories.ErrDeleted)
		return nil, false
	}
	return m, true
}

// cloneBlocks deep-copies blocks so a mutation never aliases the "before"
// document used for permission and sync diffs.
func cloneBlocks(in []*memories.Block) []*memories.Block {
	out := make([]*memories.Block, 0, len(in))
	for _, b := range in {
		nb := *b
		if b.SlideshowInterval != nil {
			v := *b.SlideshowInterval
			nb.SlideshowInterval = &v
		}
		if b.Images != nil {
			nb.Images = make([]*memories.Image, 0, len(b.Images))
			for _, img := range b.Images {
				ni := *img
				if img.Edits.Crop != nil {
					c := *img.Edits.Crop
					ni.Edits.Crop = &c
				}
				nb.Images = append(nb.Images, &ni)
			}
		}
		out = append(out, &nb)
	}
	return out
}

func findBlock(blocks []*memories.Block, id string) int {
	return slices.IndexFunc(blocks, func(b *memories.Block) bool { return b.ID == id })
}

func findImage(blocks []*memories.Block, id string) (int, int) {
	for bi, b := range blocks {
		for ii, img := range b.Images {
			if img.ID == id {
				return bi, ii
			}
		}
	}
	return -1, -1
}

func clampIndex(idx *int, n int) int {
	if idx == nil || *idx < 0 || *idx > n {
		return n
	}
	return *idx
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request, what string) {
	writeError(w, s.logger, requestIDOrEmpty(r), http.StatusNotFound, CodeNotFound, what+" not found.")
}

func (s *Server) badRequest(w http.ResponseWriter, r *http.Request, msg string) {
	writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest, CodeBadRequest, msg)
}

/* ------------------------------- handlers ------------------------------- */

// handleListMemories — GET /api/v1/libraries/{id}/memories
//
// Supports limit/cursor pagination and, when ?q= is present, full-text search
// over titles, text blocks, captions, descriptions, locations and tags.
func (s *Server) handleListMemories(w http.ResponseWriter, r *http.Request, u *auth.User) {
	mc, ok := s.openMemory(w, r, u, "", authz.CapRead)
	if !ok {
		return
	}
	defer mc.cleanup()

	q := r.URL.Query().Get("q")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	cursor := r.URL.Query().Get("cursor")

	var (
		ms   []*memories.Memory
		next string
		err  error
	)
	if q != "" {
		ms, next, err = mc.store.Search(r.Context(), q, cursor, limit)
	} else {
		ms, next, err = mc.store.List(r.Context(), cursor, limit)
	}
	if err != nil {
		if errors.Is(err, fts.ErrInvalid) {
			s.badRequest(w, r, "Invalid search query.")
			return
		}
		s.logger.Error("list memories", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to list memories.")
		return
	}
	var covers []string
	for _, m := range ms {
		covers = append(covers, m.CoverFileID)
	}
	mr, err := s.newMediaResolver(r.Context(), u, mc.lib, mc.store, covers)
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	out := make([]memoryResponse, 0, len(ms))
	for _, m := range ms {
		resp := baseMemoryResponse(m)
		if m.CoverFileID != "" {
			v := mr.view(m.CoverFileID)
			resp.Cover = &v
		}
		out = append(out, resp)
	}
	writeJSON(w, s.logger, http.StatusOK, memoryListResponse{Memories: out, Next: next})
}

// handleCreateMemory — POST /api/v1/libraries/{id}/memories
//
// Accepts either a legacy Markdown body (stored as one text block) or a
// blocks array.
func (s *Server) handleCreateMemory(w http.ResponseWriter, r *http.Request, u *auth.User) {
	mc, ok := s.openMemory(w, r, u, "", authz.CapCreate)
	if !ok {
		return
	}
	defer mc.cleanup()

	var body struct {
		Title      string       `json:"title"`
		Body       string       `json:"body"`
		MemoryDate *string      `json:"memory_date"`
		Blocks     []blockInput `json:"blocks"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}

	p := memories.CreateParams{Title: body.Title, Body: body.Body}
	if body.MemoryDate != nil {
		t, err := time.Parse(time.RFC3339, *body.MemoryDate)
		if err != nil {
			s.badRequest(w, r, "memory_date must be an RFC3339 timestamp.")
			return
		}
		p.MemoryDate = &t
	}
	if body.Blocks != nil {
		p.Blocks = make([]*memories.Block, 0, len(body.Blocks))
		for _, b := range body.Blocks {
			p.Blocks = append(p.Blocks, b.toBlock())
		}
		if !s.checkNewSources(w, r, u, mc, nil, p.Blocks) {
			return
		}
	}

	m, err := mc.store.Create(r.Context(), p)
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	doc, err := mc.store.GetDocument(r.Context(), m.ID)
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	s.respondDocument(w, r, u, mc, doc, http.StatusCreated, needsDerivedSync(nil, doc.Blocks))
}

// handleGetMemory — GET /api/v1/libraries/{id}/memories/{memoryID}
//
// Returns the memory with its ordered blocks. Images whose original went
// missing are first re-attached to a present file with the same content hash
// when one exists (a moved or re-indexed original).
func (s *Server) handleGetMemory(w http.ResponseWriter, r *http.Request, u *auth.User) {
	memoryID := r.PathValue("memoryID")
	mc, ok := s.openMemory(w, r, u, memoryID, authz.CapRead)
	if !ok {
		return
	}
	defer mc.cleanup()

	if s.can(r.Context(), u, authz.EntityKey("m", mc.lib.ID, memoryID), authz.CapEdit) {
		if _, err := mc.store.ReconcileSources(r.Context(), memoryID); err != nil &&
			!errors.Is(err, memories.ErrDeleted) && !errors.Is(err, memories.ErrNotFound) {
			s.logger.Warn("reconcile memory sources", "memory_id", memoryID, "error", err)
		}
	}
	m, err := mc.store.GetDocument(r.Context(), memoryID)
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	s.respondDocument(w, r, u, mc, m, http.StatusOK, false)
}

// handleUpdateMemory — PUT /api/v1/libraries/{id}/memories/{memoryID}
//
// Legacy whole-body save, kept for older clients. It replaces the memory's
// single text block; a memory with image blocks or several text blocks
// answers 409 (use PUT .../document instead).
func (s *Server) handleUpdateMemory(w http.ResponseWriter, r *http.Request, u *auth.User) {
	memoryID := r.PathValue("memoryID")
	mc, ok := s.openMemory(w, r, u, memoryID, authz.CapEdit)
	if !ok {
		return
	}
	defer mc.cleanup()

	var body struct {
		Title      string  `json:"title"`
		Body       string  `json:"body"`
		MemoryDate *string `json:"memory_date"`
		ClearDate  bool    `json:"clear_date"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}

	p := memories.UpdateParams{Title: body.Title, Body: body.Body, ClearDate: body.ClearDate}
	if body.MemoryDate != nil {
		t, err := time.Parse(time.RFC3339, *body.MemoryDate)
		if err != nil {
			s.badRequest(w, r, "memory_date must be an RFC3339 timestamp.")
			return
		}
		p.MemoryDate = &t
	}

	m, err := mc.store.Update(r.Context(), memoryID, p)
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"memory": baseMemoryResponse(m)})
}

// handlePatchMemory — PATCH /api/v1/libraries/{id}/memories/{memoryID}
//
// Updates metadata: title, description, location, memory_date (null clears),
// cover_file_id (null clears) and tags.
func (s *Server) handlePatchMemory(w http.ResponseWriter, r *http.Request, u *auth.User) {
	memoryID := r.PathValue("memoryID")
	mc, ok := s.openMemory(w, r, u, memoryID, authz.CapEdit)
	if !ok {
		return
	}
	defer mc.cleanup()

	var body struct {
		BaseRevision *int             `json:"base_revision"`
		Title        *string          `json:"title"`
		Description  *string          `json:"description"`
		Location     *string          `json:"location"`
		MemoryDate   optional[string] `json:"memory_date"`
		CoverFileID  optional[string] `json:"cover_file_id"`
		Tags         *[]string        `json:"tags"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	p := memories.MetaPatch{Title: body.Title, Description: body.Description, Location: body.Location, Tags: body.Tags}
	if body.MemoryDate.Set {
		if body.MemoryDate.Null || body.MemoryDate.Value == "" {
			p.ClearDate = true
		} else {
			t, err := time.Parse(time.RFC3339, body.MemoryDate.Value)
			if err != nil {
				s.badRequest(w, r, "memory_date must be an RFC3339 timestamp.")
				return
			}
			p.MemoryDate = &t
		}
	}
	if body.CoverFileID.Set {
		cover := ""
		if !body.CoverFileID.Null {
			cover = body.CoverFileID.Value
		}
		if cover != "" {
			srcs, err := mc.store.LookupSources(r.Context(), []string{cover})
			if err != nil {
				s.writeMemoryError(w, r, err)
				return
			}
			src, found := srcs[cover]
			if !found {
				s.badRequest(w, r, "cover_file_id does not name a file in this library.")
				return
			}
			if !s.requireCap(w, r, u, authz.FileKey(mc.lib.ID, src.RelPath), authz.CapRead) {
				return
			}
		}
		p.CoverFileID = &cover
	}

	if _, err := mc.store.PatchMeta(r.Context(), memoryID, body.BaseRevision, p); err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	m, err := mc.store.GetDocument(r.Context(), memoryID)
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	s.respondDocument(w, r, u, mc, m, http.StatusOK, false)
}

// handlePutMemoryDocument — PUT /api/v1/libraries/{id}/memories/{memoryID}/document
//
// Replaces the ordered blocks in one atomic, revision-checked write. This is
// what the editor's autosave sends.
func (s *Server) handlePutMemoryDocument(w http.ResponseWriter, r *http.Request, u *auth.User) {
	memoryID := r.PathValue("memoryID")
	mc, ok := s.openMemory(w, r, u, memoryID, authz.CapEdit)
	if !ok {
		return
	}
	defer mc.cleanup()

	var body struct {
		BaseRevision *int         `json:"base_revision"`
		Blocks       []blockInput `json:"blocks"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	if body.Blocks == nil {
		s.badRequest(w, r, "blocks is required.")
		return
	}
	before, ok := s.loadForMutation(w, r, mc, memoryID)
	if !ok {
		return
	}
	blocks := make([]*memories.Block, 0, len(body.Blocks))
	for _, b := range body.Blocks {
		blocks = append(blocks, b.toBlock())
	}
	s.saveAndRespond(w, r, u, mc, memoryID, body.BaseRevision, before, blocks, http.StatusOK)
}

// mutate runs a granular edit: load the document, apply fn to a copy, save.
func (s *Server) mutate(w http.ResponseWriter, r *http.Request, u *auth.User, base *int,
	fn func(blocks []*memories.Block) ([]*memories.Block, bool)) {
	memoryID := r.PathValue("memoryID")
	mc, ok := s.openMemory(w, r, u, memoryID, authz.CapEdit)
	if !ok {
		return
	}
	defer mc.cleanup()
	before, ok := s.loadForMutation(w, r, mc, memoryID)
	if !ok {
		return
	}
	blocks, ok := fn(cloneBlocks(before.Blocks))
	if !ok {
		return
	}
	s.saveAndRespond(w, r, u, mc, memoryID, base, before, blocks, http.StatusOK)
}

// handleCreateMemoryBlock — POST .../memories/{memoryID}/blocks
func (s *Server) handleCreateMemoryBlock(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var body struct {
		BaseRevision *int `json:"base_revision"`
		Index        *int `json:"index"`
		blockInput
		FileIDs []string `json:"file_ids"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	block := body.toBlock()
	if block.Type == memories.BlockImage {
		if block.Layout == "" {
			block.Layout = memories.Layout(s.memorySettingsFor(r.Context(), u).DefaultLayout)
		}
		for _, id := range body.FileIDs {
			block.Images = append(block.Images, &memories.Image{SourceFileID: id})
		}
	}
	s.mutate(w, r, u, body.BaseRevision, func(blocks []*memories.Block) ([]*memories.Block, bool) {
		return slices.Insert(blocks, clampIndex(body.Index, len(blocks)), block), true
	})
}

// handlePatchMemoryBlock — PATCH .../memories/{memoryID}/blocks/{blockID}
func (s *Server) handlePatchMemoryBlock(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var body struct {
		BaseRevision *int    `json:"base_revision"`
		Markdown     *string `json:"markdown"`
		Layout       *string `json:"layout"`
		Slideshow    *struct {
			Enabled         *bool         `json:"enabled"`
			IntervalSeconds optional[int] `json:"interval_seconds"`
		} `json:"slideshow"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	blockID := r.PathValue("blockID")
	s.mutate(w, r, u, body.BaseRevision, func(blocks []*memories.Block) ([]*memories.Block, bool) {
		i := findBlock(blocks, blockID)
		if i < 0 {
			s.notFound(w, r, "Block")
			return nil, false
		}
		b := blocks[i]
		if body.Markdown != nil {
			if b.Type != memories.BlockText {
				s.badRequest(w, r, "markdown applies to text blocks only.")
				return nil, false
			}
			b.Markdown = *body.Markdown
		}
		if (body.Layout != nil || body.Slideshow != nil) && b.Type != memories.BlockImage {
			s.badRequest(w, r, "layout and slideshow apply to image blocks only.")
			return nil, false
		}
		if body.Layout != nil {
			b.Layout = memories.Layout(*body.Layout)
		}
		if ss := body.Slideshow; ss != nil {
			if ss.Enabled != nil {
				b.Slideshow = *ss.Enabled
			}
			if ss.IntervalSeconds.Set {
				if ss.IntervalSeconds.Null {
					b.SlideshowInterval = nil
				} else {
					v := ss.IntervalSeconds.Value
					b.SlideshowInterval = &v
				}
			}
		}
		return blocks, true
	})
}

// handleDeleteMemoryBlock — DELETE .../memories/{memoryID}/blocks/{blockID}
//
// Removing an image block removes its references only; originals are never
// touched.
func (s *Server) handleDeleteMemoryBlock(w http.ResponseWriter, r *http.Request, u *auth.User) {
	blockID := r.PathValue("blockID")
	s.mutate(w, r, u, baseRevisionQuery(r), func(blocks []*memories.Block) ([]*memories.Block, bool) {
		i := findBlock(blocks, blockID)
		if i < 0 {
			s.notFound(w, r, "Block")
			return nil, false
		}
		return slices.Delete(blocks, i, i+1), true
	})
}

// handleDuplicateMemoryBlock — POST .../memories/{memoryID}/blocks/{blockID}/duplicate
//
// The copy is inserted right after the original. Image blocks duplicate their
// references and configuration, never the media.
func (s *Server) handleDuplicateMemoryBlock(w http.ResponseWriter, r *http.Request, u *auth.User) {
	blockID := r.PathValue("blockID")
	s.mutate(w, r, u, baseRevisionQuery(r), func(blocks []*memories.Block) ([]*memories.Block, bool) {
		i := findBlock(blocks, blockID)
		if i < 0 {
			s.notFound(w, r, "Block")
			return nil, false
		}
		dup := cloneBlocks(blocks[i : i+1])[0]
		dup.ID = ""
		for _, img := range dup.Images {
			img.ID = ""
			img.DerivedID = ""
		}
		return slices.Insert(blocks, i+1, dup), true
	})
}

// handleReorderMemoryBlocks — PUT .../memories/{memoryID}/blocks/order
func (s *Server) handleReorderMemoryBlocks(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var body struct {
		BaseRevision *int     `json:"base_revision"`
		BlockIDs     []string `json:"block_ids"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	s.mutate(w, r, u, body.BaseRevision, func(blocks []*memories.Block) ([]*memories.Block, bool) {
		ordered, ok := permute(blocks, body.BlockIDs, func(b *memories.Block) string { return b.ID })
		if !ok {
			s.badRequest(w, r, "block_ids must list every block of the memory exactly once.")
			return nil, false
		}
		return ordered, true
	})
}

// handleAddMemoryImages — POST .../memories/{memoryID}/blocks/{blockID}/images
//
// Adds library files to an existing image block by reference (no copies).
func (s *Server) handleAddMemoryImages(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var body struct {
		BaseRevision *int     `json:"base_revision"`
		FileIDs      []string `json:"file_ids"`
		Index        *int     `json:"index"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	if len(body.FileIDs) == 0 {
		s.badRequest(w, r, "file_ids is required.")
		return
	}
	blockID := r.PathValue("blockID")
	s.mutate(w, r, u, body.BaseRevision, func(blocks []*memories.Block) ([]*memories.Block, bool) {
		i := findBlock(blocks, blockID)
		if i < 0 || blocks[i].Type != memories.BlockImage {
			s.notFound(w, r, "Image block")
			return nil, false
		}
		added := make([]*memories.Image, 0, len(body.FileIDs))
		for _, id := range body.FileIDs {
			added = append(added, &memories.Image{SourceFileID: id})
		}
		b := blocks[i]
		b.Images = slices.Insert(b.Images, clampIndex(body.Index, len(b.Images)), added...)
		return blocks, true
	})
}

// handleReorderMemoryImages — PUT .../memories/{memoryID}/blocks/{blockID}/images/order
func (s *Server) handleReorderMemoryImages(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var body struct {
		BaseRevision *int     `json:"base_revision"`
		ImageIDs     []string `json:"image_ids"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	blockID := r.PathValue("blockID")
	s.mutate(w, r, u, body.BaseRevision, func(blocks []*memories.Block) ([]*memories.Block, bool) {
		i := findBlock(blocks, blockID)
		if i < 0 || blocks[i].Type != memories.BlockImage {
			s.notFound(w, r, "Image block")
			return nil, false
		}
		ordered, ok := permute(blocks[i].Images, body.ImageIDs, func(img *memories.Image) string { return img.ID })
		if !ok {
			s.badRequest(w, r, "image_ids must list every image of the block exactly once.")
			return nil, false
		}
		blocks[i].Images = ordered
		return blocks, true
	})
}

// handlePatchMemoryImage — PATCH .../memories/{memoryID}/images/{imageID}
//
// Changes a memory image's caption, edits, or source file (replace). The
// caption and edits belong to this memory only; the library file and its own
// metadata are never modified.
func (s *Server) handlePatchMemoryImage(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var body struct {
		BaseRevision *int                    `json:"base_revision"`
		Caption      *string                 `json:"caption"`
		FileID       *string                 `json:"file_id"`
		Crop         optional[memories.Crop] `json:"crop"`
		Rotation     *int                    `json:"rotation"`
		Filter       *string                 `json:"filter"`
		Adjustments  *memories.Adjustments   `json:"adjustments"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	imageID := r.PathValue("imageID")
	s.mutate(w, r, u, body.BaseRevision, func(blocks []*memories.Block) ([]*memories.Block, bool) {
		bi, ii := findImage(blocks, imageID)
		if bi < 0 {
			s.notFound(w, r, "Memory image")
			return nil, false
		}
		img := blocks[bi].Images[ii]
		if body.Caption != nil {
			img.Caption = *body.Caption
		}
		if body.FileID != nil && *body.FileID != img.SourceFileID {
			img.SourceFileID = *body.FileID
			img.DerivedID = ""
		}
		if body.Crop.Set {
			if body.Crop.Null {
				img.Edits.Crop = nil
			} else {
				c := body.Crop.Value
				img.Edits.Crop = &c
			}
		}
		if body.Rotation != nil {
			img.Edits.Rotation = *body.Rotation
		}
		if body.Filter != nil {
			img.Edits.Filter = *body.Filter
		}
		if body.Adjustments != nil {
			img.Edits.Adjustments = *body.Adjustments
		}
		return blocks, true
	})
}

// handleDeleteMemoryImage — DELETE .../memories/{memoryID}/images/{imageID}
//
// "Remove from memory": deletes the reference only. The library file is
// never deleted, moved or modified.
func (s *Server) handleDeleteMemoryImage(w http.ResponseWriter, r *http.Request, u *auth.User) {
	imageID := r.PathValue("imageID")
	s.mutate(w, r, u, baseRevisionQuery(r), func(blocks []*memories.Block) ([]*memories.Block, bool) {
		bi, ii := findImage(blocks, imageID)
		if bi < 0 {
			s.notFound(w, r, "Memory image")
			return nil, false
		}
		blocks[bi].Images = slices.Delete(blocks[bi].Images, ii, ii+1)
		return blocks, true
	})
}

// handleGetMemoryImageDerived — GET .../memories/{memoryID}/images/{imageID}/derived
//
// Serves the edited copy of a memory image when one exists. Requires read on
// the memory and on the source file the copy was rendered from.
func (s *Server) handleGetMemoryImageDerived(w http.ResponseWriter, r *http.Request, u *auth.User) {
	memoryID, imageID := r.PathValue("memoryID"), r.PathValue("imageID")
	mc, ok := s.openMemory(w, r, u, memoryID, authz.CapRead)
	if !ok {
		return
	}
	defer mc.cleanup()
	doc, err := mc.store.GetDocument(r.Context(), memoryID)
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	bi, ii := findImage(doc.Blocks, imageID)
	if bi < 0 {
		s.notFound(w, r, "Memory image")
		return
	}
	img := doc.Blocks[bi].Images[ii]
	srcs, err := mc.store.LookupSources(r.Context(), []string{img.SourceFileID})
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	src, found := srcs[img.SourceFileID]
	if !found {
		s.notFound(w, r, "Edited copy")
		return
	}
	if !s.requireCap(w, r, u, authz.FileKey(mc.lib.ID, src.RelPath), authz.CapRead) {
		return
	}
	f, err := mc.store.OpenDerived(r.Context(), s.deriverFor(mc.lib), memoryID, imageID)
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeContent(w, r, imageID+".jpg", f.ModTime, bytes.NewReader(f.Data))
}

// handleDeleteMemory — DELETE /api/v1/libraries/{id}/memories/{memoryID}
func (s *Server) handleDeleteMemory(w http.ResponseWriter, r *http.Request, u *auth.User) {
	mc, ok := s.openMemory(w, r, u, r.PathValue("memoryID"), authz.CapDelete)
	if !ok {
		return
	}
	defer mc.cleanup()
	if err := mc.store.Delete(r.Context(), r.PathValue("memoryID")); err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRestoreMemory — POST /api/v1/libraries/{id}/memories/{memoryID}/restore
func (s *Server) handleRestoreMemory(w http.ResponseWriter, r *http.Request, u *auth.User) {
	mc, ok := s.openMemory(w, r, u, r.PathValue("memoryID"), authz.CapEdit)
	if !ok {
		return
	}
	defer mc.cleanup()
	if err := mc.store.Restore(r.Context(), r.PathValue("memoryID")); err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// memoryVersionResponse is the API representation of a saved revision.
// Blocks is included when fetching a single version.
type memoryVersionResponse struct {
	MemoryID string          `json:"memory_id"`
	Version  int             `json:"version"`
	Title    string          `json:"title"`
	Body     string          `json:"body"`
	SavedAt  string          `json:"saved_at"`
	Blocks   []blockResponse `json:"blocks,omitempty"`
}

func toVersionResponse(v *memories.MemoryVersion) memoryVersionResponse {
	return memoryVersionResponse{
		MemoryID: v.MemoryID,
		Version:  v.Version,
		Title:    v.Title,
		Body:     v.Body,
		SavedAt:  v.SavedAt.UTC().Format(time.RFC3339),
	}
}

// handleListMemoryVersions — GET /api/v1/libraries/{id}/memories/{memoryID}/versions
func (s *Server) handleListMemoryVersions(w http.ResponseWriter, r *http.Request, u *auth.User) {
	mc, ok := s.openMemory(w, r, u, r.PathValue("memoryID"), authz.CapRead)
	if !ok {
		return
	}
	defer mc.cleanup()

	vs, err := mc.store.ListVersions(r.Context(), r.PathValue("memoryID"))
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	out := make([]memoryVersionResponse, 0, len(vs))
	for _, v := range vs {
		out = append(out, toVersionResponse(v))
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"versions": out})
}

// handleGetMemoryVersion — GET /api/v1/libraries/{id}/memories/{memoryID}/versions/{version}
func (s *Server) handleGetMemoryVersion(w http.ResponseWriter, r *http.Request, u *auth.User) {
	memoryID := r.PathValue("memoryID")
	mc, ok := s.openMemory(w, r, u, memoryID, authz.CapRead)
	if !ok {
		return
	}
	defer mc.cleanup()

	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil {
		s.badRequest(w, r, "version must be an integer.")
		return
	}
	v, err := mc.store.GetVersion(r.Context(), memoryID, version)
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	resp := toVersionResponse(v)
	if blocks, err := memories.DecodeSnapshot(v.Document, v.Body); err == nil {
		m, err := mc.store.Get(r.Context(), memoryID)
		if err != nil {
			s.writeMemoryError(w, r, err)
			return
		}
		doc, err := s.documentResponse(r.Context(), u, mc.lib, mc.store, m, blocks)
		if err != nil {
			s.writeMemoryError(w, r, err)
			return
		}
		// Historical images have no live derived copies.
		for i := range doc.Blocks {
			for j := range doc.Blocks[i].Images {
				doc.Blocks[i].Images[j].Derived = nil
			}
		}
		resp.Blocks = doc.Blocks
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"version": resp})
}

// handleListMemoryRefs — GET /api/v1/libraries/{id}/memories/{memoryID}/refs
func (s *Server) handleListMemoryRefs(w http.ResponseWriter, r *http.Request, u *auth.User) {
	mc, ok := s.openMemory(w, r, u, r.PathValue("memoryID"), authz.CapRead)
	if !ok {
		return
	}
	defer mc.cleanup()

	refs, err := mc.store.ListRefs(r.Context(), r.PathValue("memoryID"))
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"refs": toMemoryRefResponses(refs)})
}

func baseRevisionQuery(r *http.Request) *int {
	raw := r.URL.Query().Get("base_revision")
	if raw == "" {
		return nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return nil
	}
	return &v
}

// permute reorders items to match ids, which must name each item once.
func permute[T any](items []T, ids []string, key func(T) string) ([]T, bool) {
	if len(ids) != len(items) {
		return nil, false
	}
	byID := make(map[string]T, len(items))
	for _, it := range items {
		byID[key(it)] = it
	}
	out := make([]T, 0, len(items))
	for _, id := range ids {
		it, ok := byID[id]
		if !ok {
			return nil, false
		}
		delete(byID, id)
		out = append(out, it)
	}
	return out, true
}

// writeMemoryError maps memory store errors to HTTP responses.
func (s *Server) writeMemoryError(w http.ResponseWriter, r *http.Request, err error) {
	reqID := requestIDOrEmpty(r)
	var ve *memories.ValidationError
	var ce *memories.ConflictError
	switch {
	case errors.As(err, &ve):
		writeError(w, s.logger, reqID, http.StatusBadRequest, CodeBadRequest, ve.Error())
	case errors.As(err, &ce):
		writeJSON(w, s.logger, http.StatusConflict, ErrorResponse{Error: ErrorBody{
			Code:      CodeConflict,
			Message:   "This memory was changed elsewhere. Reload it to continue.",
			Details:   map[string]any{"current_revision": ce.Current},
			RequestID: reqID,
		}})
	case errors.Is(err, memories.ErrStructured):
		writeError(w, s.logger, reqID, http.StatusConflict, CodeConflict,
			"This memory has image sections; save it through the document endpoint.")
	case errors.Is(err, memories.ErrDeleted):
		writeError(w, s.logger, reqID, http.StatusConflict, CodeConflict, "This memory is deleted. Restore it first.")
	case errors.Is(err, memories.ErrNotFound):
		writeError(w, s.logger, reqID, http.StatusNotFound, CodeNotFound, "Memory not found.")
	case errors.Is(err, memories.ErrVersionNotFound):
		writeError(w, s.logger, reqID, http.StatusNotFound, CodeNotFound, "Memory version not found.")
	case errors.Is(err, memories.ErrDerivedNotFound):
		writeError(w, s.logger, reqID, http.StatusNotFound, CodeNotFound, "No edited copy exists for this image.")
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, s.logger, reqID, http.StatusNotFound, CodeNotFound, "Memory not found.")
	default:
		s.logger.Error("memory operation", "error", err)
		writeError(w, s.logger, reqID, http.StatusInternalServerError,
			CodeInternal, "Internal server error.")
	}
}
