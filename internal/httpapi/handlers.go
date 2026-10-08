package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/version"
)

// healthResponse reports on the server's ability to serve requests.
type healthResponse struct {
	Status string `json:"status"`
	DB     string `json:"database"`
}

// handleHealth reports liveness and the health of core dependencies.
//
// The endpoint is deliberately cheap: it pings the database with a short
// timeout. It is suitable for container health checks and orchestration
// probes. No application state is exposed here.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	dbStatus := "ok"
	if err := s.db.PingContext(ctx); err != nil {
		s.logger.Error("health check: database ping failed", "error", err)
		dbStatus = "error"
	}

	status := "ok"
	if dbStatus != "ok" {
		status = "degraded"
	}

	writeJSON(w, s.logger, http.StatusOK, healthResponse{
		Status: status,
		DB:     dbStatus,
	})
}

// handleVersion returns build metadata about the running binary.
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.logger, http.StatusOK, version.Get())
}

// readyResponse reports readiness to serve traffic.
type readyResponse struct {
	Status   string `json:"status"`
	Database string `json:"database"`
}

// handleReady reports whether the server is ready to serve traffic.
//
// Unlike /health (liveness: "is the process alive?"), /ready answers "should a
// load balancer / container orchestrator send traffic here?". It is 200 only
// when the server has finished startup and the database answers pings; it
// flips to 503 before graceful shutdown so proxies drain connections to the
// remaining replica while this instance stops.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if !s.ready.Load() {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusServiceUnavailable,
			CodeServiceUnavailable, "Server is shutting down.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	dbStatus := "ok"
	if err := s.db.PingContext(ctx); err != nil {
		s.logger.Error("readiness check: database ping failed", "error", err)
		dbStatus = "error"
	}

	if dbStatus != "ok" {
		writeJSON(w, s.logger, http.StatusServiceUnavailable, readyResponse{
			Status:   "unavailable",
			Database: dbStatus,
		})
		return
	}

	writeJSON(w, s.logger, http.StatusOK, readyResponse{
		Status:   "ready",
		Database: dbStatus,
	})
}

// handleMetrics renders the server metrics in Prometheus text format.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if s.metrics == nil {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusNotFound,
			CodeNotFound, "Metrics are not enabled on this server.")
		return
	}
	s.metrics.Handler().ServeHTTP(w, r)
}
