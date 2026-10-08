package httpapi

import (
	"net/http"

	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
	"github.com/Jishnu-Prasad888/Cairn/internal/metadata"
)

// handleCapabilities — GET /api/v1/system/capabilities
// Reports optional host tools the server found, so the app can say what is
// missing. Without ffmpeg, video thumbnails depend on the browser decoding
// the video itself.
func (s *Server) handleCapabilities(w http.ResponseWriter, _ *http.Request, _ *auth.User) {
	writeJSON(w, s.logger, http.StatusOK, map[string]any{
		"ffmpeg":     metadata.VideoFramesAvailable(),
		"face_model": s.mlRuntime != nil && s.mlRuntime.UsesFaceModel(),
	})
}
