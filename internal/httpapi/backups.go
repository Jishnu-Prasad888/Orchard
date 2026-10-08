package httpapi

import (
	"net/http"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
	"github.com/Jishnu-Prasad888/Cairn/internal/backups"
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
