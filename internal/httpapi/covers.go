package httpapi

import (
	"context"
	"fmt"

	"github.com/Jishnu-Prasad888/Orchard/internal/bookmeta"
	"github.com/Jishnu-Prasad888/Orchard/internal/library"
	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
	"github.com/Jishnu-Prasad888/Orchard/internal/media"
)

// coverURLsFor returns file ID -> cover URL for every document file in
// files known to have a cover, via one batch query against book_metadata —
// a grid-listing handler calls this once for its whole page rather than
// looking a cover up per file. Best-effort: a failure to open the database
// or read covers just means no file gets a cover thumbnail this time,
// never a failed listing.
func (s *Server) coverURLsFor(ctx context.Context, lib *library.Library, files []*media.File) map[string]string {
	var ids []string
	for _, f := range files {
		if f.MediaType == media.MediaTypeDocument {
			ids = append(ids, f.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	db, err := librarydb.Open(lib.Root + "/.orchard")
	if err != nil {
		return nil
	}
	defer func() { _ = db.Close() }()

	has, err := bookmeta.NewStore(db).HasCovers(ctx, ids)
	if err != nil {
		s.logger.Error("check file covers", "library_id", lib.ID, "error", err)
		return nil
	}
	out := make(map[string]string, len(has))
	for id, ok := range has {
		if ok {
			out[id] = fmt.Sprintf("/api/v1/libraries/%s/files/%s/cover", lib.ID, id)
		}
	}
	return out
}

// attachCoverURLs sets CoverURL on each response whose file has one, per
// coverURLsFor.
func attachCoverURLs(resp []fileResponse, covers map[string]string) {
	if len(covers) == 0 {
		return
	}
	for i := range resp {
		if url, ok := covers[resp[i].ID]; ok {
			resp[i].CoverURL = url
		}
	}
}
