package httpapi

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Jishnu-Prasad888/Orchard/internal/bookmeta"
	"github.com/Jishnu-Prasad888/Orchard/internal/library"
	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
	"github.com/Jishnu-Prasad888/Orchard/internal/media"
	"github.com/Jishnu-Prasad888/Orchard/internal/progress"
)

// fileExtras is what a file listing shows on a book's tile beyond the file
// itself: its cover, and how far the acting user has read.
type fileExtras struct {
	covers   map[string]string  // file ID -> cover URL
	progress map[string]float64 // file ID -> percent read (0-100)
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
// every document file in files, in one batch query each — a grid-listing
// handler calls this once for its whole page rather than per file.
// Best-effort: a failure to open the database or read either table just
// means tiles go without that detail this time, never a failed listing.
func (s *Server) fileExtrasFor(ctx context.Context, lib *library.Library, userID string, files []*media.File) fileExtras {
	var ids []string
	for _, f := range files {
		if f.MediaType == media.MediaTypeDocument {
			ids = append(ids, f.ID)
		}
	}
	if len(ids) == 0 {
		return fileExtras{}
	}
	db, err := librarydb.Open(lib.Root + "/.orchard")
	if err != nil {
		return fileExtras{}
	}
	defer func() { _ = db.Close() }()

	var extras fileExtras
	paths, err := bookmeta.NewStore(db).CoverPaths(ctx, ids)
	if err != nil {
		s.logger.Error("list file covers", "library_id", lib.ID, "error", err)
	} else {
		extras.covers = make(map[string]string, len(paths))
		for id, coverPath := range paths {
			extras.covers[id] = coverURL(lib.ID, id, coverVersion(lib, coverPath))
		}
	}
	if userID != "" {
		extras.progress, err = progress.NewStore(db).Percents(ctx, userID, ids)
		if err != nil {
			s.logger.Error("list reading progress", "library_id", lib.ID, "error", err)
		}
	}
	return extras
}

// attachFileExtras sets CoverURL and ProgressPercent on each response whose
// file has one, per fileExtrasFor.
func attachFileExtras(resp []fileResponse, extras fileExtras) {
	for i := range resp {
		if url, ok := extras.covers[resp[i].ID]; ok {
			resp[i].CoverURL = url
		}
		if pct, ok := extras.progress[resp[i].ID]; ok {
			resp[i].ProgressPercent = pct
		}
	}
}
