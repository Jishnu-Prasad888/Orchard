// Package webui serves the compiled Orchard frontend either from the embedded
// copy or, in development, from an on-disk build directory.
//
// The frontend is a React single-page application. Every non-file request is
// answered with index.html so that client-side routing keeps working after a
// direct navigation or reload.
package webui

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

//go:embed all:dist
var embedded embed.FS

// Handler returns an http.Handler that serves the Orchard frontend as a SPA.
//
// If distDir is non-empty it is served instead of the embedded assets. This
// lets a developer run a freshly built frontend against the API server
// without recompiling the binary.
func Handler(distDir string) (http.Handler, error) {
	if distDir == "" {
		root, err := fs.Sub(embedded, "dist")
		if err != nil {
			return nil, fmt.Errorf("read embedded frontend: %w", err)
		}
		if err := requireFile(root, "index.html"); err != nil {
			return nil, fmt.Errorf("embedded frontend is incomplete: %w", err)
		}
		return &spaHandler{fsys: root}, nil
	}

	info, err := os.Stat(distDir)
	if err != nil {
		return nil, fmt.Errorf("ORCHARD_WEB_DIST: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("ORCHARD_WEB_DIST %q is not a directory", distDir)
	}
	root := os.DirFS(distDir)
	if err := requireFile(root, "index.html"); err != nil {
		return nil, fmt.Errorf("ORCHARD_WEB_DIST %q is missing index.html: %w", distDir, err)
	}
	return &spaHandler{fsys: root}, nil
}

func requireFile(fsys fs.FS, name string) error {
	_, err := fs.Stat(fsys, name)
	return err
}

// spaHandler serves files from an fs.FS with single-page-app fallback.
type spaHandler struct {
	fsys fs.FS
}

func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cleaned, err := cleanPath(r.URL.Path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if cleaned == "" || cleaned == "." {
		cleaned = "index.html"
	}

	name := cleaned
	if info, err := fs.Stat(h.fsys, name); err != nil || info.IsDir() {
		name = "index.html" // a client-side route
	}

	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}

	f, encoding, err := h.open(name, r.Header.Get("Accept-Encoding"))
	if err != nil {
		http.Error(w, "frontend not available", http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()
	if encoding != "" {
		w.Header().Set("Content-Encoding", encoding)
	}
	if encoding != "" || h.hasVariant(name) {
		w.Header().Add("Vary", "Accept-Encoding")
	}
	// The content type comes from the original name (".js"), never ".br".
	// Embedded and on-disk files are both seekable, so the file streams
	// instead of being copied into memory for every request.
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		http.Error(w, "frontend not available", http.StatusInternalServerError)
		return
	}
	http.ServeContent(w, r, name, time.Time{}, rs)
}

// open returns the best representation of name the client accepts: the
// build's precompressed .br or .gz copy when present, else the file itself.
func (h *spaHandler) open(name, acceptEncoding string) (fs.File, string, error) {
	for _, enc := range []struct{ token, ext string }{{"br", ".br"}, {"gzip", ".gz"}} {
		if !acceptsEncoding(acceptEncoding, enc.token) {
			continue
		}
		if f, err := h.fsys.Open(name + enc.ext); err == nil {
			return f, enc.token, nil
		}
	}
	f, err := h.fsys.Open(name)
	return f, "", err
}

func (h *spaHandler) hasVariant(name string) bool {
	_, err := fs.Stat(h.fsys, name+".gz")
	return err == nil
}

// acceptsEncoding reports whether an Accept-Encoding header allows token
// (and does not refuse it with q=0).
func acceptsEncoding(header, token string) bool {
	for _, part := range strings.Split(header, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(name), token) {
			continue
		}
		q := strings.ReplaceAll(strings.TrimSpace(params), " ", "")
		return q != "q=0" && q != "q=0.0" && q != "q=0.00" && q != "q=0.000"
	}
	return false
}

// cleanPath converts a URL path into a safe, slash-cleaned fs.FS path.
func cleanPath(urlPath string) (string, error) {
	cleaned := path.Clean(strings.TrimPrefix(urlPath, "/"))
	if cleaned == "" || strings.HasPrefix(cleaned, "..") || path.IsAbs(cleaned) {
		return "", fs.ErrInvalid
	}
	if !fs.ValidPath(cleaned) {
		return "", fs.ErrInvalid
	}
	// Reject paths that backtrack into parent directories.
	for _, part := range strings.Split(cleaned, "/") {
		if part == ".." {
			return "", fs.ErrInvalid
		}
	}
	return cleaned, nil
}
