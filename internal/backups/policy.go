package backups

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/library"
	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
	"github.com/Jishnu-Prasad888/Orchard/internal/serversettings"
)

// How often the scheduler looks at the clock, and at what has changed. Both
// checks are a single small query, so this costs nothing on a Pi; the size
// check reads every library's index, so it runs less often.
const (
	scheduleCheckEvery = 30 * time.Second
	sizeCheckEvery     = 2 * time.Minute
	// sizeRetryAfter keeps a size-triggered run that failed (a drive that is
	// unplugged, say) from being retried every two minutes.
	sizeRetryAfter = 30 * time.Minute
)

// Policy is when backups run on their own.
type Policy struct {
	Schedule    Schedule
	SizeTrigger SizeTrigger
}

// LoadPolicy reads the saved schedule and size trigger. With no saved
// schedule, ORCHARD_BACKUP_INTERVAL_MIN (cfg.Interval) still applies, timed
// from now, as it always was.
func (m *Manager) LoadPolicy(ctx context.Context, settings *serversettings.Store) error {
	m.settings = settings
	p := Policy{Schedule: Schedule{Kind: ScheduleOff}}
	if m.cfg.Interval > 0 {
		p.Schedule = Schedule{
			Kind:         ScheduleInterval,
			EveryMinutes: int(m.cfg.Interval / time.Minute),
			Since:        time.Now().UTC(),
		}
	}
	if settings != nil {
		if raw, err := settings.GetString(ctx, ScheduleSettingKey, ""); err != nil {
			return err
		} else if raw != "" {
			var s Schedule
			if err := json.Unmarshal([]byte(raw), &s); err == nil && s.Validate() == nil {
				p.Schedule = s
			}
		}
		if raw, err := settings.GetString(ctx, SizeTriggerSettingKey, ""); err != nil {
			return err
		} else if raw != "" {
			var t SizeTrigger
			if err := json.Unmarshal([]byte(raw), &t); err == nil && t.Validate() == nil {
				p.SizeTrigger = t
			}
		}
	}
	m.mu.Lock()
	m.policy = p
	m.mu.Unlock()
	m.loadDrive(ctx)
	m.loadRemote(ctx)
	return nil
}

// loadDrive reads which drive the backup folder is on. A folder seen for the
// first time (set by ORCHARD_BACKUP_DIR, or the default) is recorded now —
// unless it already looks like a missing drive, which must not be recorded
// as "the system disk is where backups go".
func (m *Manager) loadDrive(ctx context.Context) {
	dir := m.dir()
	var rec DriveRecord
	if m.settings != nil {
		if raw, err := m.settings.GetString(ctx, DriveSettingKey, ""); err == nil && raw != "" {
			_ = json.Unmarshal([]byte(raw), &rec)
		}
	}
	if dir != "" && rec.Dir != dir {
		mounts := m.mounts()
		if checkDrive(mounts, DriveRecord{}, dir) == "" {
			rec = recordDrive(mounts, dir)
			if m.settings != nil {
				if err := m.saveSetting(ctx, DriveSettingKey, rec); err != nil {
					m.logger.Warn("save backup drive", "error", err)
				}
			}
		}
	}
	m.mu.Lock()
	m.drive = rec
	m.mu.Unlock()
}

// Policy is the schedule and size trigger in force.
func (m *Manager) Policy() Policy {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.policy
}

// SetSchedule validates, saves and applies a schedule. It counts from now:
// changing the schedule never makes a run owed in the past.
func (m *Manager) SetSchedule(ctx context.Context, s Schedule) (Schedule, error) {
	if s.Kind == "" {
		s.Kind = ScheduleOff
	}
	if err := s.Validate(); err != nil {
		return Schedule{}, err
	}
	s.Since = time.Now().UTC()
	if err := m.saveSetting(ctx, ScheduleSettingKey, s); err != nil {
		return Schedule{}, err
	}
	m.mu.Lock()
	m.policy.Schedule = s
	m.attemptedSlot = time.Time{}
	m.mu.Unlock()
	return s, nil
}

// SetSizeTrigger validates, saves and applies the size trigger.
func (m *Manager) SetSizeTrigger(ctx context.Context, t SizeTrigger) (SizeTrigger, error) {
	if err := t.Validate(); err != nil {
		return SizeTrigger{}, err
	}
	if err := m.saveSetting(ctx, SizeTriggerSettingKey, t); err != nil {
		return SizeTrigger{}, err
	}
	m.mu.Lock()
	m.policy.SizeTrigger = t
	m.sizeBackoffUntil = time.Time{}
	m.mu.Unlock()
	return t, nil
}

func (m *Manager) saveSetting(ctx context.Context, key string, v any) error {
	if m.settings == nil {
		return errors.New("settings are not available")
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return m.settings.SetString(ctx, key, string(raw))
}

// NextScheduled is when the schedule next runs a backup (zero when off). A
// time in the past means it is owed and will run within a minute.
func (m *Manager) NextScheduled(ctx context.Context) (time.Time, error) {
	last, err := m.store.LastStarted(ctx)
	if err != nil {
		return time.Time{}, err
	}
	return m.Policy().Schedule.Due(last, time.Local), nil
}

// Pending is what has been added or changed in any library since the last
// successful backup started: what the next backup will pick up.
func (m *Manager) Pending(ctx context.Context) (Pending, error) {
	since, err := m.store.LastCompletedStart(ctx)
	if err != nil {
		return Pending{}, err
	}
	libs, err := m.libs.List(ctx)
	if err != nil {
		return Pending{}, err
	}
	var p Pending
	for _, lib := range libs {
		if lib.Status == library.StatusOffline {
			continue
		}
		lp, err := pendingInLibrary(ctx, lib, since)
		if err != nil {
			m.logger.Warn("backup size check", "library_id", lib.ID, "error", err)
			continue
		}
		p.Files += lp.Files
		p.Bytes += lp.Bytes
		if lp.LastChange.After(p.LastChange) {
			p.LastChange = lp.LastChange
		}
	}
	return p, nil
}

// pendingInLibrary sums the present files the index added or changed after
// since. indexed_at moves only when a file is new, changed, moved or
// uploaded (an unchanged rescan leaves it alone), so it measures new data.
func pendingInLibrary(ctx context.Context, lib library.Library, since time.Time) (Pending, error) {
	db, release, err := librarydb.Acquire(lib.Root + "/.orchard")
	if err != nil {
		return Pending{}, err
	}
	defer release()
	var (
		p    Pending
		last *string
	)
	err = db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(size_bytes), 0), MAX(indexed_at)
		FROM indexed_files WHERE status = 'present' AND indexed_at > ?`,
		rf(since)).Scan(&p.Files, &p.Bytes, &last)
	if err != nil {
		return Pending{}, err
	}
	if last != nil {
		p.LastChange, _ = time.Parse(time.RFC3339Nano, *last)
	}
	return p, nil
}

// loop is the scheduler: the clock and the size trigger.
func (m *Manager) loop(ctx context.Context) {
	clock := time.NewTicker(scheduleCheckEvery)
	defer clock.Stop()
	var lastSizeCheck time.Time
	m.checkSchedule(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.trigger:
			m.run(4*time.Hour, TriggerManual)
		case now := <-clock.C:
			m.checkSchedule(ctx)
			if now.Sub(lastSizeCheck) >= sizeCheckEvery {
				lastSizeCheck = now
				m.checkSize(ctx, now)
			}
		}
	}
}

// checkSchedule runs the backup the clock owes, if any. A slot is tried
// once: if the run cannot even start (no destination, drive unplugged), the
// next attempt is the next slot, not every 30 seconds.
func (m *Manager) checkSchedule(ctx context.Context) {
	if m.Policy().Schedule.Kind == ScheduleOff {
		return
	}
	due, err := m.NextScheduled(ctx)
	if err != nil || due.IsZero() || time.Now().Before(due) {
		return
	}
	m.mu.Lock()
	if m.attemptedSlot.Equal(due) {
		m.mu.Unlock()
		return
	}
	m.attemptedSlot = due
	m.mu.Unlock()
	trigger := TriggerScheduled
	// More than a check late means the server was not running at the time.
	if time.Since(due) > 2*scheduleCheckEvery {
		trigger = TriggerCatchUp
	}
	m.run(6*time.Hour, trigger)
}

// checkSize starts a backup once enough new data has settled.
func (m *Manager) checkSize(ctx context.Context, now time.Time) {
	t := m.Policy().SizeTrigger
	if !t.Enabled || m.busy.Load() {
		return
	}
	m.mu.RLock()
	backoff := m.sizeBackoffUntil
	m.mu.RUnlock()
	if now.Before(backoff) {
		return
	}
	p, err := m.Pending(ctx)
	if err != nil || !t.Fires(p, now) {
		return
	}
	m.logger.Info("backup size trigger", "pending_files", p.Files, "pending_bytes", p.Bytes)
	if rec := m.run(6*time.Hour, TriggerSize); rec == nil || rec.Status != StatusCompleted {
		m.mu.Lock()
		m.sizeBackoffUntil = now.Add(sizeRetryAfter)
		m.mu.Unlock()
	}
}
