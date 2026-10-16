package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"path/filepath"

	"github.com/Jishnu-Prasad888/Orchard/internal/auth"
	"github.com/Jishnu-Prasad888/Orchard/internal/library"
	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
)

// Everything about reading a library lives in the library's own database
// (.orchard/library.db), so a library opened by a fresh Orchard install —
// or carried to another machine — comes back exactly as it was left. What
// ties that data to a person is their username: user ids are random per
// install, and the library reconnects a returning member's old id to their
// new one (librarydb.ReconnectMember) the first time they reach it.

// reconnectMember reconciles u with lib's member records, once per process.
// Failures are logged, never surfaced: a library that can't be reconciled
// right now still opens, and the next process start tries again.
func (s *Server) reconnectMember(ctx context.Context, u *auth.User, lib *library.Library) {
	if u == nil || lib == nil || lib.Status == library.StatusOffline || s.auth == nil {
		return
	}
	key := lib.ID + "|" + u.ID
	if _, done := s.reconnected.Load(key); done {
		return
	}
	db, err := librarydb.Open(filepath.Join(lib.Root, ".orchard"))
	if err != nil {
		s.logger.Warn("reconnect member: open library", "library_id", lib.ID, "error", err)
		return
	}
	defer func() { _ = db.Close() }()

	users, err := s.auth.ListUsers(ctx)
	if err != nil {
		s.logger.Warn("reconnect member: list users", "error", err)
		return
	}
	n, err := librarydb.ReconnectMember(ctx, db, u.ID, u.Username, librarydb.AccountLookup{
		Exists: func(ctx context.Context, id string) (bool, error) {
			_, err := s.auth.UserByID(ctx, id)
			if errors.Is(err, sql.ErrNoRows) {
				return false, nil
			}
			return err == nil, err
		},
		SoleAccount: len(users) == 1,
	})
	if err != nil {
		s.logger.Warn("reconnect member", "library_id", lib.ID, "user_id", u.ID, "error", err)
		return
	}
	if n > 0 {
		s.logger.Info("reconnected library data to returning member",
			"library_id", lib.ID, "username", u.Username, "previous_ids", n)
	}
	s.reconnected.Store(key, struct{}{})
}

// memberSettingsLibrary resolves the optional `?library=` a settings request
// names, for settings kept in the library alongside the reading data they
// shape. It returns nil when none is named, or it is offline or unreadable —
// the account-level copy then stands in.
func (s *Server) memberSettingsLibrary(r *http.Request, u *auth.User) *library.Library {
	id := r.URL.Query().Get("library")
	if id == "" || s.libraries == nil {
		return nil
	}
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), id)
	if err != nil || lib.Status == library.StatusOffline {
		return nil
	}
	if u.Role != auth.RoleAdmin && !s.canReadLibrary(r, u, lib.ID) {
		return nil
	}
	s.reconnectMember(r.Context(), u, lib)
	return lib
}

// getMemberSetting reads key from lib's member settings into out.
func (s *Server) getMemberSetting(ctx context.Context, lib *library.Library, userID, key string, out any) bool {
	db, err := librarydb.Open(filepath.Join(lib.Root, ".orchard"))
	if err != nil {
		s.logger.Warn("open library for member setting", "library_id", lib.ID, "error", err)
		return false
	}
	defer func() { _ = db.Close() }()
	found, err := librarydb.GetMemberSetting(ctx, db, userID, key, out)
	if err != nil {
		s.logger.Warn("get member setting", "library_id", lib.ID, "key", key, "error", err)
		return false
	}
	return found
}

// setMemberSetting writes key to lib's member settings.
func (s *Server) setMemberSetting(ctx context.Context, lib *library.Library, userID, key string, value any) error {
	db, err := librarydb.Open(filepath.Join(lib.Root, ".orchard"))
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return librarydb.SetMemberSetting(ctx, db, userID, key, value)
}
