package httpapi

import (
	"net/http"

	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
	"github.com/Jishnu-Prasad888/Cairn/internal/ml"
)

// faceModelResponse is the combined model status returned to the frontend.
// The top-level fields match the old single-model FaceModelStatus so the
// existing UI keeps working; the optional Detector sub-object is new.
type faceModelResponse struct {
	// Recognizer (ArcFace) status — top-level for back-compat.
	Installed  bool          `json:"installed"`
	State      ml.ModelState `json:"state"`
	Downloaded int64         `json:"downloaded"`
	Total      int64         `json:"total"`
	Error      string        `json:"error,omitempty"`
	URL        string        `json:"url"`
	// Detector (SCRFD) status — new field, nil when not configured.
	Detector *ml.ModelStatus `json:"detector,omitempty"`
}

func buildFaceModelResponse(faceModel, faceDetector *ml.ModelDownloader) faceModelResponse {
	var resp faceModelResponse
	if faceModel != nil {
		st := faceModel.Status()
		resp.Installed = st.Installed
		resp.State = st.State
		resp.Downloaded = st.Downloaded
		resp.Total = st.Total
		resp.Error = st.Error
		resp.URL = st.URL
	}
	if faceDetector != nil {
		st := faceDetector.Status()
		resp.Detector = &st
		// Face recognition needs both models: the top-level fields describe
		// the pair, so the UI offers the download until both are installed.
		resp.Installed = resp.Installed && st.Installed
		resp.Downloaded += st.Downloaded
		resp.Total += st.Total
		switch {
		case st.State == ml.ModelDownloading || resp.State == ml.ModelDownloading:
			resp.State = ml.ModelDownloading
		case st.State == ml.ModelFailed && resp.State != ml.ModelFailed:
			resp.State = ml.ModelFailed
			resp.Error = st.Error
			resp.URL = st.URL
		}
	}
	return resp
}

// handleGetFaceModel — GET /api/v1/ml/model (admin)
// Returns the status of the ArcFace recognizer (top-level, backward-compat)
// and the SCRFD detector (nested). Both must be installed for face recognition.
func (s *Server) handleGetFaceModel(w http.ResponseWriter, _ *http.Request, _ *auth.User) {
	writeJSON(w, s.logger, http.StatusOK, buildFaceModelResponse(s.faceModel, s.faceDetector))
}

// handleDownloadFaceModel — POST /api/v1/ml/model/download (admin)
// Starts background downloads for both models and returns the combined status.
func (s *Server) handleDownloadFaceModel(w http.ResponseWriter, _ *http.Request, _ *auth.User) {
	if s.faceDetector != nil {
		s.faceDetector.Start()
	}
	if s.faceModel != nil {
		s.faceModel.Start()
	}
	writeJSON(w, s.logger, http.StatusAccepted, buildFaceModelResponse(s.faceModel, s.faceDetector))
}
