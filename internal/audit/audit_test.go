package audit

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/Jishnu-Prasad888/Cairn/internal/db"
)

func newTestService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	pool, err := db.Open(filepath.Join(t.TempDir(), "cairn.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(pool, logger), pool
}

func TestRecordPersistsEvent(t *testing.T) {
	svc, pool := newTestService(t)
	svc.Record(context.Background(), Event{
		ActorUserID:  "user-1",
		Action:       "tests.fired",
		TargetUserID: "user-2",
		IPAddress:    "127.0.0.1",
		UserAgent:    "cairn-test",
		Details:      map[string]any{"count": 3},
	})

	var (
		action, metadata, ip string
		userID, target       sql.NullString
	)
	err := pool.QueryRow(`SELECT action, user_id, target_user_id, metadata, ip_address FROM audit_log`).
		Scan(&action, &userID, &target, &metadata, &ip)
	if err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if action != "tests.fired" {
		t.Errorf("action = %q", action)
	}
	if !userID.Valid || userID.String != "user-1" {
		t.Errorf("user_id = %+v", userID)
	}
	if !target.Valid || target.String != "user-2" {
		t.Errorf("target_user_id = %+v", target)
	}
	if ip != "127.0.0.1" {
		t.Errorf("ip = %q", ip)
	}
	if metadata != `{"count":3}` {
		t.Errorf("metadata = %q", metadata)
	}
}

func TestRecordWithoutActorStoresNull(t *testing.T) {
	svc, pool := newTestService(t)
	svc.Record(context.Background(), Event{Action: "anonymous.fired"})

	var userID sql.NullString
	if err := pool.QueryRow(`SELECT user_id FROM audit_log`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if userID.Valid {
		t.Errorf("user_id = %q, want NULL", userID.String)
	}
}

func TestRecordSurvivesDetailSerializationFailures(t *testing.T) {
	// channel values cannot be JSON-marshalled; the service must degrade to a
	// JSON stub instead of panicking.
	svc, pool := newTestService(t)
	svc.Record(context.Background(), Event{
		Action:  "tests.bad_details",
		Details: map[string]any{"bad": make(chan int)},
	})

	var n int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM audit_log`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("rows = %d, want 1", n)
	}
}

func TestRecordTruncatesLongStrings(t *testing.T) {
	svc, pool := newTestService(t)
	long := make([]byte, 4096)
	for i := range long {
		long[i] = 'x'
	}
	svc.Record(context.Background(), Event{
		Action:    "tests.truncation",
		IPAddress: string(long),
		UserAgent: string(long),
	})

	var ip, agent string
	if err := pool.QueryRow(`SELECT ip_address, user_agent FROM audit_log`).Scan(&ip, &agent); err != nil {
		t.Fatal(err)
	}
	if len(ip) > 64 {
		t.Errorf("ip length = %d, want <= 64", len(ip))
	}
	if len(agent) > 256 {
		t.Errorf("user agent length = %d, want <= 256", len(agent))
	}
}
