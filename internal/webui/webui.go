// Package webui serves the compiled Cairn frontend either from the embedded
// copy or, in development, from an on-disk build directory.
//
// The frontend is a React single-page application. Every non-file request is
// answered with index.html so that client-side routing keeps working after a
// direct navigation or reload.
package webui

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

//go:embed all:dist
var embedded embed.FS

// Handler returns an http.Handler that serves the Cairn frontend as a SPA.
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
		return nil, fmt.Errorf("CAIRN_WEB_DIST: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("CAIRN_WEB_DIST %q is not a directory", distDir)
	}
	root := os.DirFS(distDir)
	if err := requireFile(root, "index.html"); err != nil {
		return nil, fmt.Errorf("CAIRN_WEB_DIST %q is missing index.html: %w", distDir, err)
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

	data, name, err := h.readOrFallback(cleaned)
	if err != nil {
		http.Error(w, "frontend not available", http.StatusInternalServerError)
		return
	}

	if strings.HasPrefix(cleaned, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}

	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

// readOrFallback reads the requested file; if it does not exist it returns
// index.html so the SPA can handle client-side routes.
func (h *spaHandler) readOrFallback(cleaned string) ([]byte, string, error) {
	data, err := fs.ReadFile(h.fsys, cleaned)
	if err == nil {
		return data, cleaned, nil
	}
	index, indexErr := fs.ReadFile(h.fsys, "index.html")
	if indexErr != nil {
		return nil, "", fmt.Errorf("read %s: %v; fallback index.html: %w", cleaned, err, indexErr)
	}
	return index, "index.html", nil
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
