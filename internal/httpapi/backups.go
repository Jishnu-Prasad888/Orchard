package httpapi

import (
	"encoding/csv"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/auth"
	"github.com/Jishnu-Prasad888/Orchard/internal/backups"
	"github.com/Jishnu-Prasad888/Orchard/internal/backups/targets"
	"github.com/Jishnu-Prasad888/Orchard/internal/bookmeta"
	"github.com/Jishnu-Prasad888/Orchard/internal/library"
	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
)

// backupResponse is the API representation of a backup record.
type backupResponse struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	Destination   string `json:"destination"`
	StartedAt     string `json:"started_at"`
	FinishedAt    string `json:"finished_at,omitempty"`
	Libraries     int    `json:"libraries"`
	Files         int    `json:"files"`
	FilesSkipped  int    `json:"files_skipped"`
	Bytes         int64  `json:"bytes"`
	StoredBytes   int64  `json:"stored_bytes"`
	Encrypted     bool   `json:"encrypted"`
	SameDevice    bool   `json:"same_device"`
	VerifyStatus  string `json:"verify_status,omitempty"`
	VerifyChecked int    `json:"verify_checked"`
	VerifyErrors  int    `json:"verify_errors"`
	ErrorMsg      string `json:"error_msg,omitempty"`
	CreatedAt     string `json:"created_at"`
	// Trigger is why it ran: "manual", "scheduled", "catch-up" or "size".
	Trigger string `json:"trigger"`
}

func toBackupResponse(r *backups.Record) backupResponse {
	return backupResponse{
		ID:            r.ID,
		Status:        string(r.Status),
		Destination:   r.Destination,
		StartedAt:     r.StartedAt.UTC().Format(time.RFC3339),
		FinishedAt:    formatOptionalTime(r.FinishedAt),
		Libraries:     r.Libraries,
		Files:         r.Files,
		FilesSkipped:  r.FilesSkipped,
		Bytes:         r.Bytes,
		StoredBytes:   r.StoredBytes,
		Encrypted:     r.Encrypted,
		SameDevice:    r.SameDevice,
		VerifyStatus:  r.VerifyStatus,
		VerifyChecked: r.VerifyChecked,
		VerifyErrors:  r.VerifyErrors,
		ErrorMsg:      r.ErrorMsg,
		CreatedAt:     r.CreatedAt.UTC().Format(time.RFC3339),
		Trigger:       r.Trigger,
	}
}

func toBackupResponses(recs []backups.Record) []backupResponse {
	out := make([]backupResponse, 0, len(recs))
	for _, r := range recs {
		out = append(out, toBackupResponse(&r))
	}
	return out
}

func formatOptionalTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// handleRunBackup — POST /api/v1/backups
// Runs a backup now (if one is not already in progress) and returns the
// resulting record. The run is synchronous so the client can poll the record.
func (s *Server) handleRunBackup(w http.ResponseWriter, r *http.Request, u *auth.User) {
	rec, err := s.backups.Run(r.Context())
	if err != nil {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusConflict, CodeConflict,
			"Backup could not be started: "+err.Error())
		return
	}
	writeJSON(w, s.logger, http.StatusOK, toBackupResponse(rec))
}

// backupConfigResponse is where backups go and how they are kept.
type backupConfigResponse struct {
	Dir string `json:"dir"`
	// DirSource is "setting", "env", or "default" (see backups.DirFrom*).
	DirSource       string `json:"dir_source"`
	Keep            int    `json:"keep"`
	IntervalMinutes int    `json:"interval_minutes"`
	Encrypted       bool   `json:"encrypted"`
	// Drive is the folder's drive, and whether it is plugged in now.
	Drive backups.DriveStatus `json:"drive"`
	// Destination is where backups go: "local" (Dir) or "remote".
	Destination string `json:"destination"`
	// Remote is the saved network destination, if any (never a password).
	Remote *backups.RemoteStatus `json:"remote,omitempty"`
}

func (s *Server) backupConfigResponse() backupConfigResponse {
	cfg := s.backups.BackupConfig()
	return backupConfigResponse{
		Dir:             cfg.Dir,
		DirSource:       cfg.DirSource,
		Keep:            cfg.Keep,
		IntervalMinutes: int(cfg.Interval.Minutes()),
		Encrypted:       cfg.Passphrase != "",
		Drive:           s.backups.Drive(),
		Destination:     destination(s.backups),
		Remote:          remoteOf(s.backups),
	}
}

func destination(m *backups.Manager) string {
	if _, active := m.Remote(); active {
		return backups.DestinationRemote
	}
	return backups.DestinationLocal
}

func remoteOf(m *backups.Manager) *backups.RemoteStatus {
	r, _ := m.Remote()
	return r
}

// remoteRequest is a network destination to test or save. A missing secret
// means "keep the saved one" (same server and account only).
type remoteRequest struct {
	Config targets.Config  `json:"config"`
	Secret *targets.Secret `json:"secret,omitempty"`
}

// writeRemoteError explains why a network destination did not work. A
// server whose identity is not confirmed yet comes back as 409 with its
// fingerprint, so the page can ask a person to check and confirm it.
func (s *Server) writeRemoteError(w http.ResponseWriter, r *http.Request, err error) {
	var unknown *targets.UnknownServerError
	if errors.As(err, &unknown) {
		writeErrorDetails(w, s.logger, requestIDOrEmpty(r), http.StatusConflict, "SERVER_IDENTITY",
			unknown.Error(), map[string]any{"fingerprint": unknown.Fingerprint, "changed": unknown.Changed})
		return
	}
	writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest, CodeBadRequest, err.Error())
}

// handleTestBackupRemote — POST /api/v1/backups/remote/test
// Connects, writes and removes a small file; saves nothing.
func (s *Server) handleTestBackupRemote(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var body remoteRequest
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	if body.Config.Kind == targets.KindLocal {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest, CodeBadRequest,
			"Use the backup folder setting for a folder on this server.")
		return
	}
	if err := s.backups.TestRemote(r.Context(), body.Config, body.Secret); err != nil {
		s.writeRemoteError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"ok": true})
}

// handleSetBackupRemote — PUT /api/v1/backups/remote
// Tests the network destination, then saves it and sends backups there.
func (s *Server) handleSetBackupRemote(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var body remoteRequest
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	if body.Config.Kind == targets.KindLocal {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest, CodeBadRequest,
			"Use the backup folder setting for a folder on this server.")
		return
	}
	if err := s.backups.SetRemote(r.Context(), body.Config, body.Secret); err != nil {
		s.writeRemoteError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.backupConfigResponse())
}

// handleUseLocalBackups — POST /api/v1/backups/use-local
// Sends backups to the local folder again; the network settings are kept.
func (s *Server) handleUseLocalBackups(w http.ResponseWriter, r *http.Request, u *auth.User) {
	if err := s.backups.UseLocal(r.Context()); err != nil {
		s.logger.Error("use local backups", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError, CodeInternal,
			"Internal server error.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.backupConfigResponse())
}

// handleListBackupDrives — GET /api/v1/backups/drives
// The mounted drives a backup folder could go on, with their free space.
func (s *Server) handleListBackupDrives(w http.ResponseWriter, r *http.Request, u *auth.User) {
	drives := backups.Drives()
	if drives == nil {
		drives = []backups.Mount{}
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"drives": drives})
}

// handleGetBackupConfig — GET /api/v1/backups/config
func (s *Server) handleGetBackupConfig(w http.ResponseWriter, r *http.Request, u *auth.User) {
	writeJSON(w, s.logger, http.StatusOK, s.backupConfigResponse())
}

// handleSetBackupConfig — PUT /api/v1/backups/config
//
// Changes the backup directory. It is created if missing and must be
// writable; the choice is saved and outlives ORCHARD_BACKUP_DIR.
func (s *Server) handleSetBackupConfig(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var body struct {
		Dir string `json:"dir"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	if s.settings == nil {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusServiceUnavailable, CodeServiceUnavailable,
			"Settings are not available.")
		return
	}
	if err := s.backups.SetDir(body.Dir, backups.DirFromSetting); err != nil {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest, CodeBadRequest, err.Error())
		return
	}
	// Choosing a folder here means backups go there.
	if err := s.backups.UseLocal(r.Context()); err != nil {
		s.logger.Error("use local backups", "error", err)
	}
	if err := s.settings.SetString(r.Context(), backups.DirSettingKey, s.backups.BackupConfig().Dir); err != nil {
		s.logger.Error("save backup directory", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Internal server error.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.backupConfigResponse())
}

// handleListBackups — GET /api/v1/backups
func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request, u *auth.User) {
	recs, err := s.backups.List(r.Context(), 50)
	if err != nil {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to list backups.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, toBackupResponses(recs))
}

// handleGetBackup — GET /api/v1/backups/{id}
func (s *Server) handleGetBackup(w http.ResponseWriter, r *http.Request, u *auth.User) {
	rec, err := s.backups.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusNotFound, CodeNotFound,
			"No such backup.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, toBackupResponse(&rec))
}

// handleVerifyBackup — POST /api/v1/backups/{id}/verify
// Re-runs sample verification over an existing completed backup.
func (s *Server) handleVerifyBackup(w http.ResponseWriter, r *http.Request, u *auth.User) {
	rec, err := s.backups.Verify(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest, CodeBadRequest,
			"Verification failed: "+err.Error())
		return
	}
	writeJSON(w, s.logger, http.StatusOK, toBackupResponse(rec))
}

// handleRestoreBackup — POST /api/v1/backups/{id}/restore
// Restores a completed backup's contents into the given destination directory.
type restoreBackupRequest struct {
	Destination string `json:"destination"`
}

func (s *Server) handleRestoreBackup(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var req restoreBackupRequest
	if err := readJSON(w, r, &req); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	if req.Destination == "" {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest, CodeBadRequest,
			"Restore destination is required.")
		return
	}
	rec, err := s.backups.Restore(r.Context(), r.PathValue("id"), req.Destination)
	if err != nil {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest, CodeBadRequest,
			"Restore failed: "+err.Error())
		return
	}
	writeJSON(w, s.logger, http.StatusOK, toBackupResponse(rec))
}

// handleExportBookMetadata — GET /api/v1/backups/export/book-metadata.csv
//
// A human-readable catalog export, independent of the raw-SQLite backup
// path: every library's book metadata (title/author/publisher/year/isbn/
// description, and whether a cover exists), one row per book, ordered by
// library then path. Covers every library in one file rather than asking
// for one first, matching "back up now" running over every library too.
// Best-effort per library — a library this server cannot currently open
// (offline, or some other read error) is skipped rather than failing the
// whole export.
func (s *Server) handleExportBookMetadata(w http.ResponseWriter, r *http.Request, u *auth.User) {
	libs, err := s.libraries.List(r.Context())
	if err != nil {
		s.logger.Error("list libraries for book metadata export", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Internal server error.")
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="book-metadata.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{
		"library", "path", "title", "author", "publisher", "year", "isbn", "description", "has_cover",
	})

	for _, lib := range libs {
		if lib.Status == library.StatusOffline {
			continue
		}
		db, release, err := librarydb.Acquire(lib.Root + "/.orchard")
		if err != nil {
			s.logger.Error("open library db for book metadata export", "library_id", lib.ID, "error", err)
			continue
		}
		rows, err := bookmeta.NewStore(db).ListAllWithPaths(r.Context())
		release()
		if err != nil {
			s.logger.Error("list book metadata for export", "library_id", lib.ID, "error", err)
			continue
		}
		for _, m := range rows {
			_ = cw.Write([]string{
				lib.Name, m.RelPath, m.Title, m.Author, m.Publisher, m.Year, m.ISBN, m.Description,
				strconv.FormatBool(m.CoverPath != ""),
			})
		}
	}
	cw.Flush()
}

// backupPolicyBody is when backups run on their own.
type backupPolicyBody struct {
	Schedule    backups.Schedule    `json:"schedule"`
	SizeTrigger backups.SizeTrigger `json:"size_trigger"`
}

type backupPolicyResponse struct {
	backupPolicyBody
	// NextRun is when the schedule next backs up (RFC3339), empty when off.
	NextRun string `json:"next_run,omitempty"`
	// Timezone is the server's, which "daily at 03:00" is read in.
	Timezone string `json:"timezone"`
	// Pending is what changed since the last successful backup.
	Pending struct {
		Files      int    `json:"files"`
		Bytes      int64  `json:"bytes"`
		LastChange string `json:"last_change,omitempty"`
	} `json:"pending"`
}

func (s *Server) backupPolicyResponse(r *http.Request) (backupPolicyResponse, error) {
	p := s.backups.Policy()
	out := backupPolicyResponse{backupPolicyBody: backupPolicyBody{Schedule: p.Schedule, SizeTrigger: p.SizeTrigger}}
	next, err := s.backups.NextScheduled(r.Context())
	if err != nil {
		return out, err
	}
	out.NextRun = formatOptionalTime(next)
	out.Timezone = time.Now().Location().String()
	if name, _ := time.Now().Zone(); out.Timezone == "Local" {
		out.Timezone = name
	}
	pending, err := s.backups.Pending(r.Context())
	if err != nil {
		return out, err
	}
	out.Pending.Files = pending.Files
	out.Pending.Bytes = pending.Bytes
	out.Pending.LastChange = formatOptionalTime(pending.LastChange)
	return out, nil
}

// handleGetBackupPolicy — GET /api/v1/backups/policy
func (s *Server) handleGetBackupPolicy(w http.ResponseWriter, r *http.Request, u *auth.User) {
	out, err := s.backupPolicyResponse(r)
	if err != nil {
		s.logger.Error("backup policy", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Internal server error.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, out)
}

// handleSetBackupPolicy — PUT /api/v1/backups/policy
//
// Saves the schedule and the size trigger. A changed schedule counts from
// now, so saving it never makes a backup owed in the past; an unchanged one
// keeps its place.
func (s *Server) handleSetBackupPolicy(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var body backupPolicyBody
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	// Both are checked before either is saved, so a refused request changes
	// nothing.
	if body.Schedule.Kind == "" {
		body.Schedule.Kind = backups.ScheduleOff
	}
	for _, err := range []error{body.Schedule.Validate(), body.SizeTrigger.Validate()} {
		if err != nil {
			writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest, CodeBadRequest, err.Error())
			return
		}
	}
	current := s.backups.Policy()
	sched := body.Schedule
	sched.Since = current.Schedule.Since
	if sched != current.Schedule {
		if _, err := s.backups.SetSchedule(r.Context(), body.Schedule); err != nil {
			writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest, CodeBadRequest, err.Error())
			return
		}
	}
	if body.SizeTrigger != current.SizeTrigger {
		if _, err := s.backups.SetSizeTrigger(r.Context(), body.SizeTrigger); err != nil {
			writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest, CodeBadRequest, err.Error())
			return
		}
	}
	s.handleGetBackupPolicy(w, r, u)
}
