package httpapi

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"sync"
	"time"
)

// A password-protected share link sends its password in a header on every
// API call (never in the URL, so the link stays safe to paste). An <img>,
// <video> or a reader fetching a file cannot send that header, so once a
// visitor has given the right password the server remembers it for that
// link: a random id in an HttpOnly cookie scoped to the link's own path,
// looked up here. Nothing about the password is stored. A restart forgets
// every unlock; the page simply sends the password again on its next call.

const (
	shareSessionCookie = "orchard_share"
	shareSessionTTL    = 12 * time.Hour
	// shareSessionMax bounds memory on a small server; the oldest unlocks
	// are dropped first.
	shareSessionMax = 4096
)

type shareSession struct {
	shareID string
	expires time.Time
}

type shareSessions struct {
	mu   sync.Mutex
	byID map[string]shareSession
	now  func() time.Time
}

func newShareSessions() *shareSessions {
	return &shareSessions{byID: map[string]shareSession{}, now: time.Now}
}

// issue records an unlock of shareID and returns the cookie value.
func (ss *shareSessions) issue(shareID string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(raw)
	ss.mu.Lock()
	defer ss.mu.Unlock()
	now := ss.now()
	if len(ss.byID) >= shareSessionMax {
		ss.pruneLocked(now)
	}
	ss.byID[id] = shareSession{shareID: shareID, expires: now.Add(shareSessionTTL)}
	return id, nil
}

// unlocked reports whether the cookie value is a live unlock of shareID.
func (ss *shareSessions) unlocked(id, shareID string) bool {
	if id == "" {
		return false
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s, ok := ss.byID[id]
	if !ok {
		return false
	}
	if ss.now().After(s.expires) {
		delete(ss.byID, id)
		return false
	}
	return s.shareID == shareID
}

// pruneLocked drops expired unlocks, and if that frees nothing, the one
// closest to expiring.
func (ss *shareSessions) pruneLocked(now time.Time) {
	var oldestID string
	var oldest time.Time
	for id, s := range ss.byID {
		if now.After(s.expires) {
			delete(ss.byID, id)
			continue
		}
		if oldestID == "" || s.expires.Before(oldest) {
			oldestID, oldest = id, s.expires
		}
	}
	if len(ss.byID) >= shareSessionMax && oldestID != "" {
		delete(ss.byID, oldestID)
	}
}

// shareCookiePath scopes the unlock cookie to one link's routes, so a
// visitor's unlock of one link is never sent with another.
func shareCookiePath(token string) string { return "/api/v1/shares/" + token }

func (s *Server) setShareCookie(w http.ResponseWriter, r *http.Request, token, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     shareSessionCookie,
		Value:    value,
		Path:     shareCookiePath(token),
		MaxAge:   int(shareSessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.cookieSecure(r),
		SameSite: http.SameSiteStrictMode,
	})
}
