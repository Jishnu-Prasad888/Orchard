package httpapi

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowseDirs(t *testing.T) {
	admin, libs, member := newLibraryListServer(t)
	parent := filepath.Dir(libs[0].Root)
	if err := os.WriteFile(filepath.Join(parent, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(parent, ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}

	rec := admin.do(t, http.MethodGet, "/api/v1/fs/dirs?path="+url.QueryEscape(parent), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("browse = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Path   string  `json:"path"`
		Parent string  `json:"parent"`
		Dirs   []fsDir `json:"dirs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Path != parent || resp.Parent != filepath.Dir(parent) {
		t.Errorf("path/parent = %q/%q", resp.Path, resp.Parent)
	}
	names := map[string]bool{}
	for _, d := range resp.Dirs {
		names[d.Name] = true
	}
	if !names["Alpha"] || !names["Bravo"] || names["file.txt"] || names[".hidden"] {
		t.Errorf("dirs = %v, want Alpha and Bravo only", resp.Dirs)
	}

	if rec := admin.do(t, http.MethodGet, "/api/v1/fs/dirs?path=relative", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("relative path = %d, want 400", rec.Code)
	}
	if rec := admin.do(t, http.MethodGet, "/api/v1/fs/dirs?path=/definitely/not/here", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("missing path = %d, want 400", rec.Code)
	}
	if rec := member().do(t, http.MethodGet, "/api/v1/fs/dirs", nil); rec.Code != http.StatusForbidden {
		t.Errorf("member = %d, want 403", rec.Code)
	}
}

func TestParseMounts(t *testing.T) {
	info := `22 1 8:2 / / rw - ext4 /dev/sda2 rw
23 22 0:5 / /dev rw - devtmpfs udev rw
24 22 0:21 / /proc rw - proc proc rw
25 22 8:17 / /mnt/Data\040Drive rw - ext4 /dev/sdb1 rw
26 22 8:33 / /run/media/me/USB rw - exfat /dev/sdc1 rw
27 22 0:40 / /snap/core rw - squashfs /dev/loop0 ro
28 22 0:41 / /run/user/1000 rw - tmpfs tmpfs rw
29 22 8:2 / /home/me/dup rw - ext4 /dev/sda2 rw`
	got := parseMounts(bufio.NewScanner(strings.NewReader(info)), []fsDir{{Name: "File system (/)", Path: "/"}})
	paths := map[string]bool{}
	for _, d := range got {
		paths[d.Path] = true
	}
	for _, want := range []string{"/mnt/Data Drive", "/run/media/me/USB", "/home/me/dup"} {
		if !paths[want] {
			t.Errorf("missing mount %q in %v", want, got)
		}
	}
	for _, bad := range []string{"/", "/dev", "/proc", "/snap/core", "/run/user/1000"} {
		if paths[bad] {
			t.Errorf("mount %q should be hidden", bad)
		}
	}
}

func TestBrowseRootsWithoutPath(t *testing.T) {
	admin, _, _ := newLibraryListServer(t)
	rec := admin.do(t, http.MethodGet, "/api/v1/fs/dirs", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("roots = %d", rec.Code)
	}
	var resp struct {
		Roots bool    `json:"roots"`
		Dirs  []fsDir `json:"dirs"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if !resp.Roots || len(resp.Dirs) < 1 {
		t.Errorf("roots response = %+v", resp)
	}
}
