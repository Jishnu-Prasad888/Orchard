package authz

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/audit"
	"github.com/Jishnu-Prasad888/Cairn/internal/db"
)

// newTestService opens a fresh migrated server database with a user account
// and returns an authorization Service wired to it.
func newTestService(t *testing.T) (*Service, *sql.DB, string) {
	t.Helper()
	pool, err := db.Open(filepath.Join(t.TempDir(), "cairn.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(pool, logger, audit.New(pool, logger))

	userID := mustCreateUser(t, pool, "alice")
	return svc, pool, userID
}

func mustCreateUser(t *testing.T, pool *sql.DB, username string) string {
	t.Helper()
	userID := newID()
	now := rfc3339(time.Now().UTC())
	if _, err := pool.Exec(`
		INSERT INTO users (id, username, password_hash, role, is_enabled, created_at, updated_at)
		VALUES (?, ?, 'x', 'user', 1, ?, ?)`, userID, username, now, now); err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	return userID
}

func TestCanAdminEverything(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	if ok, err := svc.Can(ctx, Principal{UserID: "any", Admin: true}, LibraryKey("lib-1"), CapRead, CapManage, CapDelete); err != nil {
		t.Fatalf("Can: %v", err)
	} else if !ok {
		t.Error("admin must hold every capability on every resource")
	}
}

func TestDefaultDeny(t *testing.T) {
	svc, _, userID := newTestService(t)
	ctx := context.Background()
	p := Principal{UserID: userID}

	for _, key := range []string{LibraryKey("lib-1"), FolderKey("lib-1", "a"), FileKey("lib-1", "a/b.jpg")} {
		for _, cap := range AllCapabilities {
			if ok, err := svc.Can(ctx, p, key, cap); err != nil {
				t.Fatalf("Can(%q, %q): %v", key, cap, err)
			} else if ok {
				t.Errorf("unlisted capability must default to deny: %q on %q", cap, key)
			}
		}
	}
}

func TestReadInheritsToDescendants(t *testing.T) {
	svc, _, userID := newTestService(t)
	ctx := context.Background()

	if _, err := svc.CreateGrant(ctx, GrantInput{
		UserID:      userID,
		ResourceKey: LibraryKey("lib-1"),
		Caps:        caps(CapRead, CapDownload),
		Effect:      EffectAllow,
	}); err != nil {
		t.Fatalf("CreateGrant: %v", err)
	}

	p := Principal{UserID: userID}
	cases := []struct {
		key  string
		want bool
	}{
		{LibraryKey("lib-1"), true},
		{FolderKey("lib-1", "2024"), true},
		{FolderKey("lib-1", "2024/Japan"), true},
		{FileKey("lib-1", "2024/Japan/img.jpg"), true},
		{EntityKey("a", "lib-1", "a-1"), true},
		{LibraryKey("lib-2"), false}, // different library, no inheritance across roots
	}
	for _, c := range cases {
		ok, err := svc.Can(ctx, p, c.key, CapRead)
		if err != nil {
			t.Fatalf("Can: %v", err)
		}
		if ok != c.want {
			t.Errorf("read on %q = %v, want %v", c.key, ok, c.want)
		}
	}
}

func TestDenyBeatsInheritedAllowDespiteOrder(t *testing.T) {
	svc, _, userID := newTestService(t)
	ctx := context.Background()

	// Grant after deny: deny exists first, allow on the ancestor comes second.
	if _, err := svc.CreateGrant(ctx, GrantInput{
		UserID:      userID,
		ResourceKey: FolderKey("lib-1", "2024/Japan"),
		Caps:        caps(CapRead),
		Effect:      EffectDeny,
	}); err != nil {
		t.Fatalf("CreateGrant deny: %v", err)
	}
	if _, err := svc.CreateGrant(ctx, GrantInput{
		UserID:      userID,
		ResourceKey: LibraryKey("lib-1"),
		Caps:        caps(CapRead),
		Effect:      EffectAllow,
	}); err != nil {
		t.Fatalf("CreateGrant allow: %v", err)
	}

	p := Principal{UserID: userID}
	if ok, _ := svc.Can(ctx, p, FileKey("lib-1", "2024/Japan/img.jpg"), CapRead); ok {
		t.Error("deny on folder must override inherited library read allow")
	}
	if ok, _ := svc.Can(ctx, p, FileKey("lib-1", "2024/Kyoto/img.jpg"), CapRead); !ok {
		t.Error("grant on one folder must not block sibling folders")
	}
}

func TestMostSpecificWins(t *testing.T) {
	svc, _, userID := newTestService(t)
	ctx := context.Background()
	p := Principal{UserID: userID}

	mustGrant(t, svc, ctx, userID, LibraryKey("lib-1"), caps(CapRead), EffectAllow)
	mustGrant(t, svc, ctx, userID, FolderKey("lib-1", "2024"), caps(CapRead, CapShare), EffectAllow)
	// A deny on the deepest key adds a capability-only deny below the allow.
	mustGrant(t, svc, ctx, userID, FolderKey("lib-1", "2024/Japan"), caps(CapDelete), EffectDeny)

	if ok, _ := svc.Can(ctx, p, FolderKey("lib-1", "2024/Japan"), CapRead); !ok {
		t.Error("read must still hold on denied folder via matching allow")
	}
	if ok, _ := svc.Can(ctx, p, FolderKey("lib-1", "2024/Japan"), CapDelete); ok {
		t.Error("delete must be denied on Japan folder")
	}
	// Sibling at deeper path outside JD: deny scoped to Japan only.
	if ok, _ := svc.Can(ctx, p, FileKey("lib-1", "2024/Japan/a.jpg"), CapShare); !ok {
		t.Error("share inherited to Japan subtree must hold")
	}
}

func TestRevocationInvalidatesCachedDecisions(t *testing.T) {
	svc, _, userID := newTestService(t)
	ctx := context.Background()
	p := Principal{UserID: userID}

	mustGrant(t, svc, ctx, userID, LibraryKey("lib-1"), caps(CapRead), EffectAllow)

	// Prime the decision cache.
	if ok, _ := svc.Can(ctx, p, FileKey("lib-1", "a/b.jpg"), CapRead); !ok {
		t.Fatal("read should hold before revocation")
	}

	revoked, err := svc.CreateGrant(ctx, GrantInput{
		UserID:      userID,
		ResourceKey: FileKey("lib-1", "a/b.jpg"),
		Caps:        caps(CapRead),
		Effect:      EffectDeny,
	})
	if err != nil {
		t.Fatalf("CreateGrant deny: %v", err)
	}
	if ok, _ := svc.Can(ctx, p, FileKey("lib-1", "a/b.jpg"), CapRead); ok {
		t.Error("cached decision must be invalidated by deny on exact resource")
	}
	if ok, _ := svc.Can(ctx, p, FileKey("lib-1", "a/c.jpg"), CapRead); !ok {
		t.Error("sibling must be unaffected after invalidation")
	}

	if err := svc.RevokeGrant(ctx, revoked.ID, userID); err != nil {
		t.Fatalf("RevokeGrant: %v", err)
	}
	if ok, _ := svc.Can(ctx, p, FileKey("lib-1", "a/b.jpg"), CapRead); !ok {
		t.Error("revoking the deny must restore the inherited allow (cache invalidated)")
	}
}

func TestDenyBeatsAllowAtEqualDepthOnly(t *testing.T) {
	svc, _, userID := newTestService(t)
	ctx := context.Background()
	p := Principal{UserID: userID}

	mustGrant(t, svc, ctx, userID, EntityKey("a", "lib-1", "a-1"), caps(CapRead), EffectDeny)
	mustGrant(t, svc, ctx, userID, LibraryKey("lib-1"), caps(CapRead), EffectAllow)

	if ok, _ := svc.Can(ctx, p, EntityKey("a", "lib-1", "a-1"), CapRead); ok {
		t.Error("deny on album must beat allow on library")
	}
	if ok, _ := svc.Can(ctx, p, EntityKey("a", "lib-1", "a-2"), CapRead); !ok {
		t.Error("album a-2 must inherit library allow")
	}
}

func TestCrossLibraryIsolation(t *testing.T) {
	svc, _, userID := newTestService(t)
	ctx := context.Background()
	p := Principal{UserID: userID}

	mustGrant(t, svc, ctx, userID, LibraryKey("lib-1"), caps(CapRead, CapDownload), EffectAllow)
	// lib-10 shares the "lib-1" textual prefix but must not inherit anything.
	if ok, _ := svc.Can(ctx, p, LibraryKey("lib-10"), CapRead); ok {
		t.Error("library lib-10 must not inherit grants from lib-1")
	}
	if ok, _ := svc.Can(ctx, p, FileKey("lib-1", "foo.jpg"), CapRead); !ok {
		t.Error("grant on lib-1 must cover files at the library root")
	}
}

func TestUserGrantsAreIsolated(t *testing.T) {
	svc, pool, userID := newTestService(t)
	ctx := context.Background()
	other := mustCreateUser(t, pool, "bob")

	mustGrant(t, svc, ctx, userID, LibraryKey("lib-1"), caps(CapRead), EffectAllow)

	if ok, _ := svc.Can(ctx, Principal{UserID: other}, LibraryKey("lib-1"), CapRead); ok {
		t.Error("bob must not inherit alice's grants")
	}
}

func TestCreateGrantValidatesUserAndInput(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	for name, in := range map[string]GrantInput{
		"empty user":     {UserID: "", ResourceKey: "lib-1", Caps: caps(CapRead), Effect: EffectAllow},
		"empty resource": {UserID: "u-1", ResourceKey: "", Caps: caps(CapRead), Effect: EffectAllow},
		"empty caps":     {UserID: "u-1", ResourceKey: "lib-1", Caps: CapSet{}, Effect: EffectAllow},
		"bad effect":     {UserID: "u-1", ResourceKey: "lib-1", Caps: caps(CapRead), Effect: "maybe"},
		"phantom user":   {UserID: "does-not-exist", ResourceKey: "lib-1", Caps: caps(CapRead), Effect: EffectAllow},
	} {
		if _, err := svc.CreateGrant(ctx, in); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

func TestGrantUpsertIsIdempotent(t *testing.T) {
	svc, _, userID := newTestService(t)
	ctx := context.Background()

	mustGrant(t, svc, ctx, userID, LibraryKey("lib-1"), caps(CapRead), EffectAllow)
	mustGrant(t, svc, ctx, userID, LibraryKey("lib-1"), caps(CapRead, CapDownload), EffectAllow)

	list, err := svc.ListGrants(ctx, LibraryKey("lib-1"))
	if err != nil {
		t.Fatalf("ListGrants: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("upsert produced %d grants, want 1", len(list))
	}
	// Re-granting must upgrade, not duplicate.
	if ok, _ := svc.Can(ctx, Principal{UserID: userID}, LibraryKey("lib-1"), CapDownload); !ok {
		t.Error("download must be granted after upsert upgrade")
	}
}

func TestShareTokenOnlyShownOnce(t *testing.T) {
	svc, _, userID := newTestService(t)
	ctx := context.Background()

	view, err := svc.CreateShare(ctx, NewShareInput{
		ResourceKey: LibraryKey("lib-1"),
		Caps:        caps(CapRead),
		CreatedBy:   userID,
	})
	if err != nil {
		t.Fatalf("CreateShare: %v", err)
	}
	if view.Token == "" {
		t.Fatal("raw token must be returned exactly on creation")
	}

	shares, err := svc.ListShares(ctx, LibraryKey("lib-1"))
	if err != nil {
		t.Fatalf("ListShares: %v", err)
	}
	if len(shares) != 1 {
		t.Fatalf("shares = %d, want 1", len(shares))
	}
	if shares[0].TokenHash == view.Token {
		t.Error("persisted share must store a hash, never the raw token")
	}
	if shares[0].TokenHash == "" {
		t.Error("token hash must be stored")
	}
}

func TestShareAuthAndAccess(t *testing.T) {
	svc, _, userID := newTestService(t)
	ctx := context.Background()

	view, err := svc.CreateShare(ctx, NewShareInput{
		ResourceKey: LibraryKey("lib-1"),
		Caps:        caps(CapRead, CapDownload),
		CreatedBy:   userID,
	})
	if err != nil {
		t.Fatalf("CreateShare: %v", err)
	}

	sh, err := svc.AuthenticateShare(ctx, view.Token, "")
	if err != nil {
		t.Fatalf("AuthenticateShare: %v", err)
	}
	if !sh.Capabilities.All(CapRead) || !sh.Capabilities.All(CapDownload) {
		t.Errorf("share caps = %v", sh.Capabilities)
	}
	if _, err := svc.AuthenticateShare(ctx, view.Token+"x", ""); err != ErrUnauthorized {
		t.Errorf("wrong token: err = %v, want ErrUnauthorized", err)
	}
}

func TestSharePasswordRequired(t *testing.T) {
	svc, _, userID := newTestService(t)
	ctx := context.Background()

	view, err := svc.CreateShare(ctx, NewShareInput{
		ResourceKey: LibraryKey("lib-1"),
		Caps:        caps(CapRead),
		Password:    "s3cret-pass",
		CreatedBy:   userID,
	})
	if err != nil {
		t.Fatalf("CreateShare: %v", err)
	}

	if _, err := svc.AuthenticateShare(ctx, view.Token, ""); err != ErrUnauthorized {
		t.Errorf("share without password: err = %v, want ErrUnauthorized", err)
	}
	if _, err := svc.AuthenticateShare(ctx, view.Token, "wrong"); err != ErrUnauthorized {
		t.Errorf("share with wrong password: err = %v, want ErrUnauthorized", err)
	}
	sh, err := svc.AuthenticateShare(ctx, view.Token, "s3cret-pass")
	if err != nil {
		t.Fatalf("share with correct password: %v", err)
	}
	if !sh.Capabilities.All(CapRead) {
		t.Error("share caps must be intact after password auth")
	}
}

func TestShareExpiry(t *testing.T) {
	svc, _, userID := newTestService(t)
	ctx := context.Background()

	exp := time.Now().UTC().Add(-time.Hour)
	view, err := svc.CreateShare(ctx, NewShareInput{
		ResourceKey: LibraryKey("lib-1"),
		Caps:        caps(CapRead),
		ExpiresAt:   &exp,
		CreatedBy:   userID,
	})
	if err != nil {
		t.Fatalf("CreateShare: %v", err)
	}
	if _, err := svc.AuthenticateShare(ctx, view.Token, ""); err != ErrUnauthorized {
		t.Errorf("expired share: err = %v, want ErrUnauthorized", err)
	}
}

func TestShareRevocationImmediate(t *testing.T) {
	svc, _, userID := newTestService(t)
	ctx := context.Background()

	view, err := svc.CreateShare(ctx, NewShareInput{
		ResourceKey: LibraryKey("lib-1"),
		Caps:        caps(CapRead),
		CreatedBy:   userID,
	})
	if err != nil {
		t.Fatalf("CreateShare: %v", err)
	}
	if _, err := svc.AuthenticateShare(ctx, view.Token, ""); err != nil {
		t.Fatalf("share before revocation: %v", err)
	}
	if err := svc.RevokeShare(ctx, view.ID, userID); err != nil {
		t.Fatalf("RevokeShare: %v", err)
	}
	if _, err := svc.AuthenticateShare(ctx, view.Token, ""); err != ErrUnauthorized {
		t.Errorf("revoked share: err = %v, want ErrUnauthorized", err)
	}
}

func TestShareValidation(t *testing.T) {
	svc, _, userID := newTestService(t)
	ctx := context.Background()

	for name, in := range map[string]NewShareInput{
		"empty resource": {ResourceKey: "", Caps: caps(CapRead), CreatedBy: userID},
		"empty caps":     {ResourceKey: "lib-1", Caps: CapSet{}, CreatedBy: userID},
		"short password": {ResourceKey: "lib-1", Caps: caps(CapRead), Password: "short", CreatedBy: userID},
	} {
		if _, err := svc.CreateShare(ctx, in); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

func caps(cs ...Capability) CapSet {
	set := make(CapSet, len(cs))
	for _, c := range cs {
		set[c] = true
	}
	return set
}

func mustGrant(t *testing.T, svc *Service, ctx context.Context, userID, key string, set CapSet, eff Effect) {
	t.Helper()
	if _, err := svc.CreateGrant(ctx, GrantInput{
		UserID: userID, ResourceKey: key, Caps: set, Effect: eff,
	}); err != nil {
		t.Fatalf("CreateGrant: %v", err)
	}
}
