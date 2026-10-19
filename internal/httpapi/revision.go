package httpapi

import (
	"net/http"
	"strings"
	"sync/atomic"
)

// countLibraryEdits notes every successful change made under
// /api/v1/libraries/{id}/ — a rename, a favorite, an upload, a quote — so the
// library's index status revision moves and other open pages re-read. Pages
// used to re-fetch everything on a timer to catch these; now they poll one
// cheap status call and fetch only when something changed.
func (s *Server) countLibraryEdits(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isMutation(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if rec.status >= 400 {
			return
		}
		rest, ok := strings.CutPrefix(r.URL.Path, "/api/v1/libraries/")
		if !ok {
			return
		}
		if id, _, _ := strings.Cut(rest, "/"); id != "" {
			s.libraryEditCount(id).Add(1)
		}
	})
}

func (s *Server) libraryEditCount(id string) *atomic.Int64 {
	n, _ := s.libraryEdits.LoadOrStore(id, new(atomic.Int64))
	return n.(*atomic.Int64)
}
