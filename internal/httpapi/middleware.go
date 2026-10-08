package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/metrics"
)

// maxRequestIDLen bounds the length of client-supplied request IDs so the log
// and response headers cannot be abused as a memory sink.
const maxRequestIDLen = 128

// WithMiddleware wraps the given handler in the standard middleware stack:
// request IDs, panic recovery, security headers, access logging, and the
// same-origin guard. The stack runs in the order listed here, so recovery
// wraps the application handler most closely.
func WithMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return requestID(recoverPanics(logger, securityHeaders(accessLog(logger, sameOrigin(logger, next)))))
}

// metricsRecording observes every request handled by the server and feeds the
// shared metrics registry. It is a no-op when reg is nil so callers that do
// not opt into metrics keep the exact previous behavior.
func metricsRecording(reg *metrics.Registry, next http.Handler) http.Handler {
	if reg == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reg.AddInFlight(1)
		defer reg.AddInFlight(-1)

		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		reg.Observe(r.Method, rec.status, time.Since(start))
	})
}

// requestID ensures every request has a request ID, preferring a
// well-formed client-supplied X-Request-ID header and otherwise generating a
// random one. The ID is stored in the request context and echoed back on the
// response.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !validRequestID(id) {
			id = newRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(WithRequestID(r.Context(), id)))
	})
}

// newRequestID returns a hex-encoded 128-bit random identifier.
func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}

func validRequestID(id string) bool {
	if len(id) == 0 || len(id) > maxRequestIDLen {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == ':':
		default:
			return false
		}
	}
	return true
}

// recoverPanics converts panics in downstream handlers into JSON 500 errors.
// The request is never allowed to die with an empty or partial response.
func recoverPanics(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				requestID := requestIDOrEmpty(r)
				logger.Error("panic in handler",
					"panic", v,
					"request_id", requestID,
					"method", r.Method,
					"path", redactPath(r.URL.Path),
				)
				// If the handler already started writing, the status code
				// cannot be changed; the panic is reported to the client only
				// when possible.
				writeError(w, logger, requestID, http.StatusInternalServerError, CodeInternal, "Internal server error.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// accessLog emits one structured log line per request after it completes.
func accessLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		logger.Info("http request",
			"request_id", requestIDOrEmpty(r),
			"method", r.Method,
			"path", redactPath(r.URL.Path),
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr,
		)
	})
}

// statusRecorder captures the response status code for access logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func requestIDOrEmpty(r *http.Request) string {
	id, _ := RequestIDFrom(r.Context())
	return id
}

// redactPath masks share bearer tokens carried in the URL path so access and
// error logs never leak them. Public share routes are mounted under
// /api/v1/shares/{token}/..; the token segment is replaced wholesale.
func redactPath(p string) string {
	const prefix = "/api/v1/shares/"
	if !strings.HasPrefix(p, prefix) {
		return p
	}
	rest := strings.TrimPrefix(p, prefix)
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		slash = len(rest)
	}
	return prefix + "[redacted]" + rest[slash:]
}

// securityHeaders sets baseline browser hardening headers on every response.
// The embedded SPA is self-contained (no inline scripts, no external origins),
// so a 'self' content security policy does not require nonces.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; media-src 'self' blob: data:; "+
				"style-src 'self' 'unsafe-inline'; script-src 'self'; "+
				"frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// sameOrigin rejects cross-origin state-changing requests. SameSite=Lax on
// the session cookie is the primary CSRF defense; this origin check is
// defense-in-depth for non-cookie-carrying clients and for methods the cookie
// policy does not cover. Browsers send Origin on cross-site POST/PUT/PATCH/
// DELETE; curl and other non-browser clients omit it and are unaffected.
func sameOrigin(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isMutation(r.Method) && !originAllowed(r) {
			writeError(w, logger, requestIDOrEmpty(r), http.StatusForbidden,
				CodeForbidden, "Cross-origin requests are not allowed.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isMutation(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// originAllowed decides whether an Origin header may trigger state changes;
// a missing Origin (curl, servers) is always allowed.
func originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" {
		return false
	}
	return sameHostPort(u, r)
}

// sameHostPort matches an Origin against the request host, tolerating the
// default-port-for-scheme cases a TLS-terminating proxy produces (host header
// "localhost:8080" with Origin "https://localhost").
func sameHostPort(o *url.URL, r *http.Request) bool {
	ohost := o.Hostname()
	rhost, rport := splitHost(r.Host)
	if ohost == "" || !strings.EqualFold(ohost, rhost) {
		return false
	}
	oport := o.Port()
	if oport == "" {
		oport = defaultPort(o.Scheme)
	}
	if rport == "" {
		proto := r.Header.Get("X-Forwarded-Proto")
		if proto == "" {
			proto = "http"
		}
		rport = defaultPort(proto)
	}
	return oport == rport
}

// splitHost splits a Host header value into hostname and port, returning an
// empty port when none is present.
func splitHost(host string) (string, string) {
	if host == "" {
		return "", ""
	}
	if h, p, err := net.SplitHostPort(host); err == nil {
		return h, p
	}
	return host, ""
}

func defaultPort(scheme string) string {
	switch strings.ToLower(scheme) {
	case "https", "wss":
		return "443"
	case "http", "ws":
		return "80"
	}
	return ""
}
