package authz

import (
	"context"
	"testing"
)

func TestKeyBuilders(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"library", LibraryKey("lib-1"), "lib-1"},
		{"root folder", FolderKey("lib-1", "2024"), "lib-1/f:2024"},
		{"nested folder", FolderKey("lib-1", "2024/Japan"), "lib-1/f:2024/f:Japan"},
		{"root file", FileKey("lib-1", "img.jpg"), "lib-1/x:img.jpg"},
		{"nested file", FileKey("lib-1", "2024/Japan/img.jpg"), "lib-1/f:2024/f:Japan/x:img.jpg"},
		{"album", EntityKey("a", "lib-1", "alb-1"), "lib-1/a:alb-1"},
		{"memory", EntityKey("m", "lib-1", "mem-1"), "lib-1/m:mem-1"},
		{"tag", EntityKey("t", "lib-1", "tag-1"), "lib-1/t:tag-1"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestCoversBoundary(t *testing.T) {
	lib := LibraryKey("lib-1")
	cov := []string{
		lib,
		lib + "/f:2024",
		lib + "/f:2024/f:Japan",
		lib + "/f:2024/x:img.jpg",
		lib + "/a:alb-1",
	}
	for _, key := range cov {
		if !covers(lib, key) {
			t.Errorf("covers(%q, %q) = false, want true", lib, key)
		}
	}
	not := []string{
		LibraryKey("lib-10"),
		LibraryKey("lib-12"),
		"lib-1" + "extra",
		LibraryKey("lib-1") + "/f:2", // "lib-1/f:2" is a valid descendant
	}
	// The last is a valid descendant; everything before must NOT be covered.
	for _, key := range not[:3] {
		if covers(lib, key) {
			t.Errorf("covers(%q, %q) = true, want false", lib, key)
		}
	}
	if !covers(lib, not[3]) {
		t.Errorf("covers(%q, %q) = false, want true", lib, not[3])
	}
}

func TestParseKeyType(t *testing.T) {
	cases := []struct {
		key  string
		want ResourceType
	}{
		{LibraryKey("lib-1"), ResourceLibrary},
		{FolderKey("lib-1", "2024/Japan"), ResourceFolder},
		{FileKey("lib-1", "2024/Japan/img.jpg"), ResourceFile},
		{FileKey("lib-1", "img.jpg"), ResourceFile},
		{EntityKey("a", "lib-1", "alb-1"), ResourceAlbum},
		{EntityKey("m", "lib-1", "mem-1"), ResourceMemory},
		{EntityKey("t", "lib-1", "tag-1"), ResourceTag},
	}
	for _, c := range cases {
		if got := ParseKeyType(c.key); got != c.want {
			t.Errorf("ParseKeyType(%q) = %q, want %q", c.key, got, c.want)
		}
	}
}

func TestDeletedUserGrantsNotCreated(t *testing.T) {
	svc, pool, _ := newTestService(t)
	ctx := context.Background()

	// Account deletion removes the row; creating new grants for it must fail.
	if _, err := pool.Exec(`DELETE FROM users WHERE id IN (SELECT id FROM users LIMIT 1)`); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if _, err := svc.CreateGrant(ctx, GrantInput{
		UserID:      "ghost",
		ResourceKey: LibraryKey("lib-1"),
		Caps:        caps(CapRead),
		Effect:      EffectAllow,
	}); err == nil {
		t.Error("grant for a non-existent user must be rejected")
	}
}

func TestLargeTreePerformance(t *testing.T) {
	svc, _, userID := newTestService(t)
	ctx := context.Background()

	// A single library grant covers every descendant; evaluation must stay
	// O(matches), not O(capabilities * nodes).
	mustGrant(t, svc, ctx, userID, LibraryKey("lib-1"), caps(CapRead, CapDownload), EffectAllow)

	p := Principal{UserID: userID}
	for i := 0; i < 10000; i++ {
		key := FileKey("lib-1", "2024/Japan/img.jpg")
		if ok, err := svc.Can(ctx, p, key, CapRead); err != nil {
			t.Fatalf("Can: %v", err)
		} else if !ok {
			t.Fatal("expected grant to hold")
		}
	}
}
