package backups

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
	"github.com/Jishnu-Prasad888/Orchard/internal/serversettings"
)

func countRuns(t *testing.T, m *Manager, trigger string) int {
	t.Helper()
	recs, err := m.List(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range recs {
		if trigger == "" || r.Trigger == trigger {
			n++
		}
	}
	return n
}

func TestPolicy_SurvivesARestart(t *testing.T) {
	pool, serverPath := tempServerDB(t)
	settings := serversettings.NewStore(pool)
	ctx := context.Background()

	m := newManager(t, pool, serverPath, t.TempDir(), "", 4)
	if err := m.LoadPolicy(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetSchedule(ctx, Schedule{Kind: ScheduleWeekly, At: "02:30", Weekday: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetSizeTrigger(ctx, SizeTrigger{Enabled: true, Bytes: 500 << 20, QuietMinutes: 15}); err != nil {
		t.Fatal(err)
	}

	// A new process on the same database.
	again := newManager(t, pool, serverPath, t.TempDir(), "", 4)
	if err := again.LoadPolicy(ctx, settings); err != nil {
		t.Fatal(err)
	}
	p := again.Policy()
	if p.Schedule.Kind != ScheduleWeekly || p.Schedule.At != "02:30" || p.Schedule.Since.IsZero() {
		t.Errorf("schedule after restart = %+v", p.Schedule)
	}
	if !p.SizeTrigger.Enabled || p.SizeTrigger.Bytes != 500<<20 || p.SizeTrigger.QuietMinutes != 15 {
		t.Errorf("size trigger after restart = %+v", p.SizeTrigger)
	}
}

func TestPolicy_EnvIntervalStillWorksUntilAScheduleIsSaved(t *testing.T) {
	pool, serverPath := tempServerDB(t)
	m := newManager(t, pool, serverPath, t.TempDir(), "", 4)
	m.cfg.Interval = 90 * time.Minute
	if err := m.LoadPolicy(context.Background(), serversettings.NewStore(pool)); err != nil {
		t.Fatal(err)
	}
	if s := m.Policy().Schedule; s.Kind != ScheduleInterval || s.EveryMinutes != 90 {
		t.Errorf("schedule from ORCHARD_BACKUP_INTERVAL_MIN = %+v", s)
	}
}

func TestPolicy_MissedRunCatchesUpOnce(t *testing.T) {
	pool, serverPath := tempServerDB(t)
	settings := serversettings.NewStore(pool)
	ctx := context.Background()
	m := newManager(t, pool, serverPath, t.TempDir(), "", 4)

	// An hourly schedule set three hours ago, on a server that was off since.
	stale, _ := json.Marshal(Schedule{Kind: ScheduleInterval, EveryMinutes: 60, Since: time.Now().Add(-3 * time.Hour)})
	if err := settings.SetString(ctx, ScheduleSettingKey, string(stale)); err != nil {
		t.Fatal(err)
	}
	if err := m.LoadPolicy(ctx, settings); err != nil {
		t.Fatal(err)
	}

	m.checkSchedule(ctx)
	if n := countRuns(t, m, TriggerCatchUp); n != 1 {
		t.Fatalf("catch-up runs = %d, want 1", n)
	}
	// Owed three times over, but it runs once; the next is an hour out.
	m.checkSchedule(ctx)
	m.checkSchedule(ctx)
	if n := countRuns(t, m, ""); n != 1 {
		t.Errorf("runs after more checks = %d, want still 1", n)
	}
	next, err := m.NextScheduled(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Until(next); d < 55*time.Minute || d > 61*time.Minute {
		t.Errorf("next run in %s, want about an hour", d)
	}
}

func TestPolicy_UnstartableSlotIsNotRetriedInALoop(t *testing.T) {
	pool, serverPath := tempServerDB(t)
	settings := serversettings.NewStore(pool)
	ctx := context.Background()
	m := newManager(t, pool, serverPath, "", "", 4) // no destination set
	stale, _ := json.Marshal(Schedule{Kind: ScheduleInterval, EveryMinutes: 60, Since: time.Now().Add(-2 * time.Hour)})
	_ = settings.SetString(ctx, ScheduleSettingKey, string(stale))
	if err := m.LoadPolicy(ctx, settings); err != nil {
		t.Fatal(err)
	}
	m.checkSchedule(ctx)
	if m.attemptedSlot.IsZero() {
		t.Fatal("the owed slot should be marked as tried")
	}
	slot := m.attemptedSlot
	m.checkSchedule(ctx)
	if !m.attemptedSlot.Equal(slot) {
		t.Error("the same slot was tried again")
	}
}

func TestPolicy_SizeTriggerBacksUpNewDataOnceItSettles(t *testing.T) {
	pool, serverPath := tempServerDB(t)
	settings := serversettings.NewStore(pool)
	ctx := context.Background()
	m := newManager(t, pool, serverPath, t.TempDir(), "", 4)
	lib := registerLibrary(t, m.libs, filepath.Join(t.TempDir(), "lib"))
	if err := m.LoadPolicy(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetSizeTrigger(ctx, SizeTrigger{Enabled: true, Bytes: 3 << 20, QuietMinutes: 5}); err != nil {
		t.Fatal(err)
	}

	ldb, release, err := librarydb.Acquire(filepath.Join(lib.Root, ".orchard"))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	add := func(id string, size int64, indexed time.Time) {
		t.Helper()
		ts := indexed.UTC().Format(time.RFC3339Nano)
		if _, err := ldb.Exec(`INSERT INTO indexed_files
			(id, rel_path, size_bytes, mod_time, status, first_seen_at, last_seen_at, indexed_at)
			VALUES (?, ?, ?, ?, 'present', ?, ?, ?)`, id, id+".bin", size, ts, ts, ts, ts); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	// 2 MB arrived 20 minutes ago: under the limit.
	add("a", 1<<20, now.Add(-20*time.Minute))
	add("b", 1<<20, now.Add(-20*time.Minute))
	m.checkSize(ctx, now)
	if n := countRuns(t, m, TriggerSize); n != 0 {
		t.Fatalf("ran under the limit: %d", n)
	}
	// Two more files push it past 3 MB, the last one a minute ago: wait.
	add("c", 1<<20, now.Add(-20*time.Minute))
	add("d", 1<<20, now.Add(-time.Minute))
	if p, _ := m.Pending(ctx); p.Files != 4 || p.Bytes != 4<<20 {
		t.Errorf("pending = %+v, want 4 files, 4 MB", p)
	}
	m.checkSize(ctx, now)
	if n := countRuns(t, m, TriggerSize); n != 0 {
		t.Fatal("ran while files were still arriving")
	}
	// Five quiet minutes later it runs, once.
	later := now.Add(6 * time.Minute)
	m.checkSize(ctx, later)
	if n := countRuns(t, m, TriggerSize); n != 1 {
		t.Fatalf("size-triggered runs = %d, want 1", n)
	}
	if p, _ := m.Pending(ctx); p.Bytes != 0 {
		t.Errorf("after the backup, pending = %+v, want nothing", p)
	}
	m.checkSize(ctx, later.Add(time.Hour))
	if n := countRuns(t, m, TriggerSize); n != 1 {
		t.Errorf("ran again with nothing new: %d", n)
	}
}
