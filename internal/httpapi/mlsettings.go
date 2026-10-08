package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
	"github.com/Jishnu-Prasad888/Cairn/internal/ml"
)

type mlSettings struct {
	// Enabled is the master switch for similarity and people recognition.
	Enabled bool `json:"enabled"`
	// FaceThreshold is how alike two faces must be (cosine similarity) to be
	// grouped as one person; FaceThresholdDefault is the model's own value.
	FaceThreshold        float64 `json:"face_threshold"`
	FaceThresholdDefault float64 `json:"face_threshold_default"`
	// BatchSize is how many new, unprocessed images must pile up (uploads or
	// files moved in by hand) before the models run on them.
	BatchSize int `json:"batch_size"`
}

type mlSettingsUpdate struct {
	Enabled *bool `json:"enabled"`
	// FaceThreshold 0 restores the default.
	FaceThreshold *float64 `json:"face_threshold"`
	BatchSize     *int     `json:"batch_size"`
}

func (s *Server) currentMLSettings() mlSettings {
	cur, def := s.mlRuntime.FaceThreshold()
	return mlSettings{Enabled: s.mlRuntime.Enabled(), FaceThreshold: cur, FaceThresholdDefault: def,
		BatchSize: s.mlRuntime.BatchSize()}
}

// handleGetMLSettings — GET /api/v1/ml/settings
// Anyone signed in may read the switch: the web app hides People when it is off.
func (s *Server) handleGetMLSettings(w http.ResponseWriter, _ *http.Request, _ *auth.User) {
	writeJSON(w, s.logger, http.StatusOK, s.currentMLSettings())
}

// handlePutMLSettings — PUT /api/v1/ml/settings (admin)
// Either field may be sent alone. Turning ML on starts processing every online
// library at once; new images are processed as they are indexed.
func (s *Server) handlePutMLSettings(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	var in mlSettingsUpdate
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&in); err != nil ||
		(in.Enabled == nil && in.FaceThreshold == nil && in.BatchSize == nil) {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest,
			CodeBadRequest, "Body must contain \"enabled\", \"face_threshold\" and/or \"batch_size\".")
		return
	}
	if t := in.FaceThreshold; t != nil {
		if *t < 0 || *t >= 1 {
			writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest,
				CodeBadRequest, "face_threshold must be between 0 and 1 (0 restores the default).")
			return
		}
		if err := s.mlRuntime.SetFaceThreshold(r.Context(), *t); err != nil {
			s.logger.Error("save face threshold", "error", err)
			writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
				CodeInternal, "Could not save the setting.")
			return
		}
	}
	if n := in.BatchSize; n != nil {
		if *n < 1 || *n > ml.MaxBatchSize {
			writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest,
				CodeBadRequest, fmt.Sprintf("batch_size must be between 1 and %d.", ml.MaxBatchSize))
			return
		}
		if err := s.mlRuntime.SetBatchSize(r.Context(), *n); err != nil {
			s.logger.Error("save ml batch size", "error", err)
			writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
				CodeInternal, "Could not save the setting.")
			return
		}
	}
	if in.Enabled != nil {
		if err := s.mlRuntime.Set(r.Context(), *in.Enabled); err != nil {
			s.logger.Error("save ml setting", "error", err)
			writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
				CodeInternal, "Could not save the setting.")
			return
		}
	}
	writeJSON(w, s.logger, http.StatusOK, s.currentMLSettings())
}
