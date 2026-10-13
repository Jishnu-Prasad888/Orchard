// Package recentsearch tracks, per user, the search queries they have
// recently run, in a per-library SQLite database — the server-side,
// per-user counterpart to the client-only localStorage recent-searches
// list, so the list follows a person across devices.
package recentsearch

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// MaxPerUser caps how many recent searches are kept per user; older entries
// are trimmed on write rather than left to grow unbounded.
const MaxPerUser = 20

// Entry is one remembered search query.
type Entry struct {
	Query      string
	SearchedAt time.Time
}

// Store is the repository for recent searches in a per-library database.
type Store struct {
	db *sql.DB
}

// NewStore wraps a per-library database pool.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Record remembers that userID just searched for query, upserting by exact
// query text so repeating a search bumps it to the top rather than
// duplicating it, then trims to the most recent MaxPerUser entries. A
// blank query is ignored.
func (s *Store) Record(ctx context.Context, userID, query string) error {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO recent_searches (user_id, query, searched_at) VALUES (?, ?, ?)
		ON CONFLICT(user_id, query) DO UPDATE SET searched_at = excluded.searched_at`,
		userID, query, now)
	if err != nil {
		return fmt.Errorf("record recent search: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		DELETE FROM recent_searches
		WHERE user_id = ? AND query NOT IN (
			SELECT query FROM recent_searches WHERE user_id = ?
			ORDER BY searched_at DESC LIMIT ?
		)`, userID, userID, MaxPerUser)
	if err != nil {
		return fmt.Errorf("trim recent searches: %w", err)
	}
	return nil
}

// List returns userID's most recently searched queries, newest first,
// capped at limit (defaults to and is capped at MaxPerUser).
func (s *Store) List(ctx context.Context, userID string, limit int) ([]Entry, error) {
	if limit <= 0 || limit > MaxPerUser {
		limit = MaxPerUser
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT query, searched_at FROM recent_searches
		WHERE user_id = ? ORDER BY searched_at DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent searches: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Entry
	for rows.Next() {
		var e Entry
		var searchedAt string
		if err := rows.Scan(&e.Query, &searchedAt); err != nil {
			return nil, err
		}
		if t, perr := time.Parse(time.RFC3339Nano, searchedAt); perr == nil {
			e.SearchedAt = t
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Forget removes a single remembered query for userID.
func (s *Store) Forget(ctx context.Context, userID, query string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM recent_searches WHERE user_id = ? AND query = ?`, userID, query)
	if err != nil {
		return fmt.Errorf("forget recent search: %w", err)
	}
	return nil
}

// Clear removes every remembered query for userID.
func (s *Store) Clear(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM recent_searches WHERE user_id = ?`, userID)
	if err != nil {
		return fmt.Errorf("clear recent searches: %w", err)
	}
	return nil
}
