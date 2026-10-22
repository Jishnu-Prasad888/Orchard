package backups

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Settings keys for when backups run on their own. Both are JSON in the
// server settings table, so they outlive restarts; the last run is read from
// the backups table itself, which is what lets a schedule survive the server
// being off: whatever was due while it was down runs once, soon after start.
const (
	ScheduleSettingKey    = "backups.schedule"
	SizeTriggerSettingKey = "backups.size_trigger"
	// DriveSettingKey is which drive the backup folder was chosen on.
	DriveSettingKey = "backups.drive"
)

// Schedule kinds.
const (
	ScheduleOff      = "off"
	ScheduleInterval = "interval"
	ScheduleDaily    = "daily"
	ScheduleWeekly   = "weekly"
)

// Schedule is when backups run by the clock.
type Schedule struct {
	Kind string `json:"kind"`
	// EveryMinutes is the gap between runs for "interval" (the app offers
	// whole hours; ORCHARD_BACKUP_INTERVAL_MIN can set any minute count).
	EveryMinutes int `json:"every_minutes,omitempty"`
	// At is "HH:MM" in the server's local time, for "daily" and "weekly".
	At string `json:"at,omitempty"`
	// Weekday is 0 (Sunday) to 6, for "weekly".
	Weekday int `json:"weekday,omitempty"`
	// Since is when this schedule was set: the starting point before any
	// backup has run under it.
	Since time.Time `json:"since"`
}

// Validate reports a schedule a person could not have meant.
func (s Schedule) Validate() error {
	switch s.Kind {
	case ScheduleOff:
		return nil
	case ScheduleInterval:
		if s.EveryMinutes < 15 || s.EveryMinutes > 60*24*31 {
			return errors.New("the interval must be between 15 minutes and 31 days")
		}
		return nil
	case ScheduleDaily, ScheduleWeekly:
		if _, _, err := parseClock(s.At); err != nil {
			return err
		}
		if s.Kind == ScheduleWeekly && (s.Weekday < 0 || s.Weekday > 6) {
			return errors.New("weekday must be 0 (Sunday) to 6 (Saturday)")
		}
		return nil
	}
	return fmt.Errorf(`schedule kind must be %q, %q, %q or %q`,
		ScheduleOff, ScheduleInterval, ScheduleDaily, ScheduleWeekly)
}

// Next is the first scheduled time strictly after last, in loc. Zero for an
// "off" schedule.
func (s Schedule) Next(last time.Time, loc *time.Location) time.Time {
	switch s.Kind {
	case ScheduleInterval:
		return last.Add(time.Duration(s.EveryMinutes) * time.Minute)
	case ScheduleDaily, ScheduleWeekly:
		h, m, err := parseClock(s.At)
		if err != nil {
			return time.Time{}
		}
		l := last.In(loc)
		t := time.Date(l.Year(), l.Month(), l.Day(), h, m, 0, 0, loc)
		for !t.After(last) || (s.Kind == ScheduleWeekly && int(t.Weekday()) != s.Weekday) {
			t = t.AddDate(0, 0, 1)
			t = time.Date(t.Year(), t.Month(), t.Day(), h, m, 0, 0, loc)
		}
		return t
	}
	return time.Time{}
}

// Due is when the next scheduled run is owed, given the last backup's start
// (zero if none ran since the schedule was set). A time in the past means it
// was missed — the server was off — and should run now.
func (s Schedule) Due(lastRun time.Time, loc *time.Location) time.Time {
	if s.Kind == ScheduleOff || s.Kind == "" {
		return time.Time{}
	}
	from := s.Since
	if lastRun.After(from) {
		from = lastRun
	}
	return s.Next(from, loc)
}

func parseClock(at string) (int, int, error) {
	hh, mm, ok := strings.Cut(at, ":")
	h, errH := strconv.Atoi(hh)
	m, errM := strconv.Atoi(mm)
	if !ok || errH != nil || errM != nil || h < 0 || h > 23 || m < 0 || m > 59 || len(mm) != 2 {
		return 0, 0, errors.New(`time must be "HH:MM" (24-hour)`)
	}
	return h, m, nil
}

// SizeTrigger starts a backup when enough has changed since the last one:
// files added or changed since then add up to at least Bytes — one large
// file or many small ones. It waits until nothing has changed for
// QuietMinutes, so a batch of uploads is backed up once, whole.
type SizeTrigger struct {
	Enabled      bool  `json:"enabled"`
	Bytes        int64 `json:"bytes"`
	QuietMinutes int   `json:"quiet_minutes"`
}

// Validate reports a trigger that would fire constantly or never.
func (t SizeTrigger) Validate() error {
	if !t.Enabled {
		return nil
	}
	if t.Bytes < 1<<20 {
		return errors.New("the size limit must be at least 1 MB")
	}
	if t.QuietMinutes < 1 || t.QuietMinutes > 24*60 {
		return errors.New("the quiet period must be between 1 minute and 24 hours")
	}
	return nil
}

// Pending is what has changed since the last backup, for the size trigger
// and for showing on the Backups page.
type Pending struct {
	Files      int
	Bytes      int64
	LastChange time.Time
}

// Fires reports whether p should start a backup at now.
func (t SizeTrigger) Fires(p Pending, now time.Time) bool {
	return t.Enabled && p.Bytes >= t.Bytes &&
		!p.LastChange.IsZero() && now.Sub(p.LastChange) >= time.Duration(t.QuietMinutes)*time.Minute
}
