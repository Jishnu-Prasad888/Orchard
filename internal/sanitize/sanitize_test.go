package sanitize

import (
	"errors"
	"testing"
)

func TestRelPath(t *testing.T) {
	ok := map[string]string{
		"photo.jpg":          "photo.jpg",
		"2022/img.jpg":       "2022/img.jpg",
		"a/b/c.mp4":          "a/b/c.mp4",
		"2022/photos/a.jpg":  "2022/photos/a.jpg",
		"with space/x 1.jpg": "with space/x 1.jpg",
		"2022/":              "2022",
		"a//b//c.mp4":        "a/b/c.mp4",
		"emoji/📷.jpg":        "emoji/📷.jpg",
		"...jpg":             "...jpg",
		"a.b/c":              "a.b/c",
	}
	for in, want := range ok {
		got, err := RelPath(in)
		if err != nil {
			t.Errorf("RelPath(%q) unexpected error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("RelPath(%q) = %q, want %q", in, got, want)
		}
	}

	bad := map[string]error{
		"":               ErrEmpty,
		".":              ErrTraversal,
		"..":             ErrTraversal,
		"./x.jpg":        ErrTraversal,
		"../escape":      ErrTraversal,
		"../x.jpg":       ErrTraversal,
		"a/../../b":      ErrTraversal,
		"a/./b":          ErrTraversal,
		"/absolute":      ErrAbsolute,
		"/abs/x.jpg":     ErrAbsolute,
		".cairn":         ErrReserved,
		".cairn/db":      ErrReserved,
		".cairn/trash/y": ErrReserved,
		"a\x00b":         ErrControl,
		"a\nb":           ErrControl,
		"\x1fb":          ErrControl,
	}
	for in, wantErr := range bad {
		_, err := RelPath(in)
		if !errors.Is(err, wantErr) {
			t.Errorf("RelPath(%q) error = %v, want %v", in, err, wantErr)
		}
	}
}

func TestBareName(t *testing.T) {
	ok := []string{"photo.jpg", "2022-01.jpg", "with space.png", "ünïcode.txt", "...jpg", "a.b"}
	for _, in := range ok {
		if _, err := BareName(in); err != nil {
			t.Errorf("BareName(%q) unexpected error: %v", in, err)
		}
	}

	bad := map[string]error{
		"":         ErrEmpty,
		".":        ErrTraversal,
		"..":       ErrTraversal,
		"a/b":      ErrNotBare,
		"a\\b":     ErrNotBare,
		"dir/name": ErrNotBare,
		".cairn":   ErrReserved,
		"a\x00b":   ErrControl,
		"a\nb":     ErrControl,
	}
	for in, wantErr := range bad {
		_, err := BareName(in)
		if !errors.Is(err, wantErr) {
			t.Errorf("BareName(%q) error = %v, want %v", in, err, wantErr)
		}
	}
}
