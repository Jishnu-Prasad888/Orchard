package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/auth"
	"github.com/Jishnu-Prasad888/Orchard/internal/authz"
	"github.com/Jishnu-Prasad888/Orchard/internal/bookmeta"
	"github.com/Jishnu-Prasad888/Orchard/internal/library"
	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
	"github.com/Jishnu-Prasad888/Orchard/internal/media"
	"github.com/Jishnu-Prasad888/Orchard/internal/progress"
	"github.com/Jishnu-Prasad888/Orchard/internal/reading"
	"github.com/Jishnu-Prasad888/Orchard/internal/tags"
	"github.com/Jishnu-Prasad888/Orchard/internal/thumbs"
)

// fileExtras is what a file listing shows on a book's tile beyond the file
// itself: its cover, how far the acting user has read, and whether they have
// marked it completed.
type fileExtras struct {
	covers     map[string]string   // file ID -> cover URL
	progress   map[string]float64  // file ID -> percent read (0-100)
	thumbnails map[string]string   // file ID -> photo thumbnail URL
	completed  map[string]bool     // file ID -> marked completed by the user
	titles     map[string]string   // file ID -> book title, when known
	tags       map[string][]string // file ID -> tag names
}

// thumbnailURL is the address of a photo's thumbnail; version names the
// source contents it was made from, so it is safe to cache for good.
func thumbnailURL(libraryID, fileID, version string) string {
	return fmt.Sprintf("/api/v1/libraries/%s/files/%s/thumbnail?v=%s",
		libraryID, fileID, url.QueryEscape(version))
}

// coverURL is the address of a file's cover. version changes whenever the
// image does, so a browser may cache the response indefinitely and still
// show a replaced cover immediately.
func coverURL(libraryID, fileID string, version int64) string {
	return fmt.Sprintf("/api/v1/libraries/%s/files/%s/cover?v=%d", libraryID, fileID, version)
}

// coverVersion is the cover image's modification time, or 0 when the file
// cannot be read (the URL still works; it just is not cache-busted).
func coverVersion(lib *library.Library, coverPath string) int64 {
	info, err := os.Stat(filepath.Join(lib.Root, ".orchard", filepath.FromSlash(coverPath)))
	if err != nil {
		return 0
	}
	return info.ModTime().Unix()
}

// fileExtrasFor looks up covers and the acting user's reading progress for
// every document file in files, and which files of any kind they have marked
// completed, in one batch query each — a grid-listing
// handler calls this once for its whole page rather than per file.
// Best-effort: a failure to open the database or read either table just
// means tiles go without that detail this time, never a failed listing.
func (s *Server) fileExtrasFor(ctx context.Context, lib *library.Library, userID string, files []*media.File) fileExtras {
	var ids, photos, all []string
	for _, f := range files {
		all = append(all, f.ID)
		switch f.MediaType {
		case media.MediaTypeDocument:
			ids = append(ids, f.ID)
		case media.MediaTypePhoto:
			photos = append(photos, f.ID)
		}
	}
	// Tags and completion apply to any kind of file.
	if len(all) == 0 {
		return fileExtras{}
	}
	db, release, err := librarydb.Acquire(lib.Root + "/.orchard")
	if err != nil {
		return fileExtras{}
	}
	defer release()

	var extras fileExtras
	extras.tags, err = tags.NewTagStore(db).NamesForFiles(ctx, all)
	if err != nil {
		s.logger.Error("list file tags", "library_id", lib.ID, "error", err)
	}
	if userID != "" {
		extras.completed, err = reading.NewStore(db).CompletedAmong(ctx, userID, all)
		if err != nil {
			s.logger.Error("list completed files", "library_id", lib.ID, "error", err)
		}
	}
	if len(photos) > 0 {
		versions, err := thumbs.Versions(ctx, db, photos)
		if err != nil {
			s.logger.Error("list thumbnails", "library_id", lib.ID, "error", err)
		}
		extras.thumbnails = make(map[string]string, len(versions))
		for id, v := range versions {
			extras.thumbnails[id] = thumbnailURL(lib.ID, id, v)
		}
	}
	if len(ids) == 0 {
		return extras
	}
	paths, err := bookmeta.NewStore(db).CoverPaths(ctx, ids)
	if err != nil {
		s.logger.Error("list file covers", "library_id", lib.ID, "error", err)
	} else {
		extras.covers = make(map[string]string, len(paths))
		for id, coverPath := range paths {
			extras.covers[id] = coverURL(lib.ID, id, coverVersion(lib, coverPath))
		}
	}
	extras.titles, err = bookmeta.NewStore(db).Titles(ctx, ids)
	if err != nil {
		s.logger.Error("list book titles", "library_id", lib.ID, "error", err)
	}
	if userID != "" {
		extras.progress, err = progress.NewStore(db).Percents(ctx, userID, ids)
		if err != nil {
			s.logger.Error("list reading progress", "library_id", lib.ID, "error", err)
		}
	}
	return extras
}

// attachFileExtras sets CoverURL, ProgressPercent, ThumbnailURL and Completed
// on each response whose file has one, per fileExtrasFor.
func attachFileExtras(resp []fileResponse, extras fileExtras) {
	for i := range resp {
		if url, ok := extras.covers[resp[i].ID]; ok {
			resp[i].CoverURL = url
		}
		if pct, ok := extras.progress[resp[i].ID]; ok {
			resp[i].ProgressPercent = pct
		}
		if url, ok := extras.thumbnails[resp[i].ID]; ok {
			resp[i].ThumbnailURL = url
		}
		resp[i].Completed = extras.completed[resp[i].ID]
		resp[i].Title = extras.titles[resp[i].ID]
		resp[i].Tags = extras.tags[resp[i].ID]
	}
}

// handleGetThumbnail — GET /api/v1/libraries/{id}/files/{fileID}/thumbnail
//
// It needs the same permission as downloading the photo: a grid tile used to
// load the original, so this never shows anyone more than it did.
func (s *Server) handleGetThumbnail(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	fileID := r.PathValue("fileID")
	if !s.fileKeyFor(w, r, u, lib, fileID, authz.CapDownload) {
		return
	}
	db, release, err := librarydb.Acquire(filepath.Join(lib.Root, ".orchard"))
	if err != nil {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError, CodeInternal,
			"Failed to open library database.")
		return
	}
	rel, err := thumbs.Path(r.Context(), db, fileID)
	release()
	if err != nil || rel == "" {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusNotFound, CodeNotFound, "No thumbnail yet.")
		return
	}
	f, err := os.Open(filepath.Join(lib.Root, ".orchard", filepath.FromSlash(rel)))
	if err != nil {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusNotFound, CodeNotFound, "No thumbnail yet.")
		return
	}
	defer func() { _ = f.Close() }()
	w.Header().Set("Content-Type", "image/jpeg")
	if r.URL.Query().Get("v") != "" {
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "private, no-cache")
	}
	http.ServeContent(w, r, "thumbnail.jpg", time.Time{}, f)
}
