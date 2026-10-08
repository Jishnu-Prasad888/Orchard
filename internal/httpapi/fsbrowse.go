package httpapi

import (
	"bufio"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
)

// fsDir is one sub-directory offered by the folder browser.
type fsDir struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// handleBrowseDirs lists the sub-directories of a path on the server, so the
// web client can offer a folder chooser when registering a library. Admin only
// (the same caller may already register any path), directories only, and
// read-only. With no path it lists the places to start from: the home
// directory, the filesystem root, and every mounted drive.
//
// GET /api/v1/fs/dirs?path=/mnt
func (s *Server) handleBrowseDirs(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	target := strings.TrimSpace(r.URL.Query().Get("path"))
	if target == "" {
		writeJSON(w, s.logger, http.StatusOK, map[string]any{
			"path":   "",
			"parent": "",
			"roots":  true,
			"dirs":   browseRoots(),
		})
		return
	}
	if !filepath.IsAbs(target) {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest, "invalid_path", "path must be absolute")
		return
	}
	target = filepath.Clean(target)

	entries, err := os.ReadDir(target)
	if err != nil {
		status := http.StatusBadRequest
		if os.IsPermission(err) {
			status = http.StatusForbidden
		}
		writeError(w, s.logger, requestIDOrEmpty(r), status, "unreadable_path", "cannot read that folder")
		return
	}

	dirs := make([]fsDir, 0, len(entries))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		full := filepath.Join(target, e.Name())
		// Follow symlinks so a linked mount point shows up as a folder.
		info, err := os.Stat(full)
		if err != nil || !info.IsDir() {
			continue
		}
		dirs = append(dirs, fsDir{Name: e.Name(), Path: full})
	}
	sort.Slice(dirs, func(i, j int) bool {
		return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name)
	})

	parent := filepath.Dir(target)
	if parent == target {
		parent = ""
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{
		"path":   target,
		"parent": parent,
		"dirs":   dirs,
	})
}

// pseudoFS are filesystem types that never hold a person's media.
var pseudoFS = map[string]bool{
	"proc": true, "sysfs": true, "devtmpfs": true, "devpts": true, "tmpfs": true,
	"cgroup": true, "cgroup2": true, "securityfs": true, "debugfs": true,
	"tracefs": true, "pstore": true, "bpf": true, "configfs": true,
	"fusectl": true, "mqueue": true, "hugetlbfs": true, "autofs": true,
	"binfmt_misc": true, "overlay": true, "squashfs": true, "efivarfs": true,
	"ramfs": true, "nsfs": true, "rpc_pipefs": true, "fuse.gvfsd-fuse": true,
	"fuse.portal": true, "fuse.snapfuse": true,
}

// browseRoots lists where the folder chooser can start: home, the filesystem
// root, and each mounted drive found in /proc/self/mountinfo.
func browseRoots() []fsDir {
	roots := []fsDir{}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, fsDir{Name: "Home", Path: home})
	}
	roots = append(roots, fsDir{Name: "File system (/)", Path: "/"})
	if f, err := os.Open("/proc/self/mountinfo"); err == nil {
		defer func() { _ = f.Close() }()
		roots = append(roots, parseMounts(bufio.NewScanner(f), roots)...)
	}
	return roots
}

// parseMounts extracts real drive mount points from mountinfo lines, skipping
// pseudo filesystems, system paths, and anything already in `have`.
func parseMounts(sc *bufio.Scanner, have []fsDir) []fsDir {
	seen := map[string]bool{}
	for _, h := range have {
		seen[h.Path] = true
	}
	var out []fsDir
	for sc.Scan() {
		line := sc.Text()
		head, tail, ok := strings.Cut(line, " - ")
		if !ok {
			continue
		}
		fields := strings.Fields(head)
		tailFields := strings.Fields(tail)
		if len(fields) < 5 || len(tailFields) < 1 {
			continue
		}
		mount := unescapeMount(fields[4])
		if seen[mount] || pseudoFS[tailFields[0]] || hiddenMount(mount) {
			continue
		}
		seen[mount] = true
		out = append(out, fsDir{Name: mount, Path: mount})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// hiddenMount reports system mount points that are never a media location.
func hiddenMount(p string) bool {
	for _, prefix := range []string{"/proc", "/sys", "/dev", "/snap", "/boot", "/var/lib", "/run"} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return !strings.HasPrefix(p, "/run/media")
		}
	}
	return false
}

// unescapeMount decodes the \040-style octal escapes mountinfo uses.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+4 <= len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
