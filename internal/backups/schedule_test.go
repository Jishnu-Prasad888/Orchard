package backups

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("no tzdata for %s: %v", name, err)
	}
	return loc
}

func TestSchedule_Next(t *testing.T) {
	loc := mustLoc(t, "Asia/Kolkata")
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	cases := []struct {
		name string
		s    Schedule
		last string
		want string
	}{
		{"interval", Schedule{Kind: ScheduleInterval, EveryMinutes: 360}, "2026-10-22 01:30", "2026-10-22 07:30"},
		{"daily later today", Schedule{Kind: ScheduleDaily, At: "03:00"}, "2026-10-22 01:30", "2026-10-22 03:00"},
		{"daily tomorrow", Schedule{Kind: ScheduleDaily, At: "03:00"}, "2026-10-22 03:00", "2026-10-23 03:00"},
		{"weekly next monday", Schedule{Kind: ScheduleWeekly, At: "02:15", Weekday: 1}, "2026-10-22 09:00", "2026-10-26 02:15"},
		{"weekly same day earlier", Schedule{Kind: ScheduleWeekly, At: "23:00", Weekday: 4}, "2026-10-22 09:00", "2026-10-22 23:00"},
	}
	for _, c := range cases {
		if got := c.s.Next(at(c.last), loc); !got.Equal(at(c.want)) {
			t.Errorf("%s: Next(%s) = %s, want %s", c.name, c.last, got.In(loc), c.want)
		}
	}
}

func TestSchedule_DueCatchesUpAfterDowntime(t *testing.T) {
	loc := time.UTC
	since := time.Date(2026, 10, 1, 12, 0, 0, 0, loc)
	s := Schedule{Kind: ScheduleDaily, At: "03:00", Since: since}

	// Nothing has run yet: the first run is the next 03:00 after it was set.
	if got := s.Due(time.Time{}, loc); !got.Equal(time.Date(2026, 10, 2, 3, 0, 0, 0, loc)) {
		t.Errorf("first due = %s", got)
	}
	// Last ran on the 5th; the server was off until the 9th. The run owed on
	// the 6th is in the past, so it is due now — once: after it runs, the
	// next is computed from that run, not replayed for every missed day.
	last := time.Date(2026, 10, 5, 3, 0, 4, 0, loc)
	due := s.Due(last, loc)
	if !due.Equal(time.Date(2026, 10, 6, 3, 0, 0, 0, loc)) {
		t.Fatalf("due after downtime = %s", due)
	}
	ranAt := time.Date(2026, 10, 9, 18, 0, 0, 0, loc)
	if got := s.Due(ranAt, loc); !got.Equal(time.Date(2026, 10, 10, 3, 0, 0, 0, loc)) {
		t.Errorf("after catching up, next = %s, want the 10th at 03:00", got)
	}
	if !(Schedule{Kind: ScheduleOff}).Due(last, loc).IsZero() {
		t.Error("an off schedule is never due")
	}
}

func TestSchedule_Validate(t *testing.T) {
	bad := []Schedule{
		{Kind: "hourly"},
		{Kind: ScheduleInterval, EveryMinutes: 5},
		{Kind: ScheduleDaily, At: "25:00"},
		{Kind: ScheduleDaily, At: "3:0"},
		{Kind: ScheduleWeekly, At: "03:00", Weekday: 7},
	}
	for _, s := range bad {
		if s.Validate() == nil {
			t.Errorf("%+v validated", s)
		}
	}
	for _, s := range []Schedule{{Kind: ScheduleOff}, {Kind: ScheduleInterval, EveryMinutes: 60},
		{Kind: ScheduleDaily, At: "00:00"}, {Kind: ScheduleWeekly, At: "23:59", Weekday: 6}} {
		if err := s.Validate(); err != nil {
			t.Errorf("%+v: %v", s, err)
		}
	}
}

func TestSizeTrigger_FiresOnceQuiet(t *testing.T) {
	now := time.Now()
	tr := SizeTrigger{Enabled: true, Bytes: 100 << 20, QuietMinutes: 10}
	big := Pending{Files: 1, Bytes: 150 << 20, LastChange: now.Add(-11 * time.Minute)}
	if !tr.Fires(big, now) {
		t.Error("one large new file past the limit, quiet for long enough, should fire")
	}
	many := Pending{Files: 300, Bytes: 120 << 20, LastChange: now.Add(-11 * time.Minute)}
	if !tr.Fires(many, now) {
		t.Error("many small files adding up past the limit should fire")
	}
	if tr.Fires(Pending{Files: 3, Bytes: 120 << 20, LastChange: now.Add(-2 * time.Minute)}, now) {
		t.Error("should wait for the quiet period while files are still arriving")
	}
	if tr.Fires(Pending{Files: 3, Bytes: 10 << 20, LastChange: now.Add(-time.Hour)}, now) {
		t.Error("under the limit should not fire")
	}
	if (SizeTrigger{Bytes: 1}).Fires(big, now) {
		t.Error("a disabled trigger should not fire")
	}
}
