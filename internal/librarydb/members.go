package librarydb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// A library keeps everything about reading it — progress, bookmarks,
// highlights, quotes, notes, collections, favorites, preferences — in its own
// database, keyed by the Orchard user_id of whoever made it. user_ids are
// random per install, though, so a library opened on a fresh install (or a
// second machine) would find none of that data belonging to anyone.
//
// library_members fixes that by remembering each user_id's username. When a
// member reaches the library under a new user_id with a username the library
// already knows, ReconnectMember moves the old id's rows to the new one.

// AccountLookup tells ReconnectMember about the accounts of the Orchard
// install the library is currently opened by.
type AccountLookup struct {
	// Exists reports whether a user_id is a live account on this install.
	// Data of a live account is never moved to someone else.
	Exists func(ctx context.Context, userID string) (bool, error)
	// SoleAccount is true when this install has exactly one account. Only
	// then may data predating library_members (ids the library has no
	// username for) be reconnected — when there is just one orphaned id and
	// one person it can belong to.
	SoleAccount bool
}

// ReconnectMember records userID as username in the library, first moving
// over the data of any earlier user_id that belonged to the same person on
// another install. It returns how many earlier ids were reconnected.
func ReconnectMember(ctx context.Context, db *sql.DB, userID, username string, accounts AccountLookup) (int, error) {
	if userID == "" || username == "" {
		return 0, errors.New("reconnect member: user id and username are required")
	}
	tables, err := userScopedTables(ctx, db)
	if err != nil {
		return 0, err
	}

	previous, err := previousIDs(ctx, db, tables, userID, username, accounts)
	if err != nil {
		return 0, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("reconnect member: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, old := range previous {
		if _, err := tx.ExecContext(ctx, `DELETE FROM library_members WHERE user_id = ?`, old); err != nil {
			return 0, fmt.Errorf("reconnect member: forget old id: %w", err)
		}
		for _, table := range tables {
			// Rows the new id already has (made before reconnecting) win;
			// the old copy of such a row is dropped rather than duplicated.
			if _, err := tx.ExecContext(ctx,
				fmt.Sprintf(`UPDATE OR IGNORE %s SET user_id = ? WHERE user_id = ?`, quoteIdent(table)),
				userID, old); err != nil {
				return 0, fmt.Errorf("reconnect member: move %s: %w", table, err)
			}
			if _, err := tx.ExecContext(ctx,
				fmt.Sprintf(`DELETE FROM %s WHERE user_id = ?`, quoteIdent(table)), old); err != nil {
				return 0, fmt.Errorf("reconnect member: clear %s: %w", table, err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO library_members (user_id, username, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET username = excluded.username, updated_at = excluded.updated_at`,
		userID, username, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return 0, fmt.Errorf("reconnect member: record: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("reconnect member: commit: %w", err)
	}
	return len(previous), nil
}

// previousIDs lists the earlier user_ids whose data belongs to this member:
// ids recorded under the same username, plus — on a single-account install
// — the one id that predates library_members, if there is exactly one.
// Ids that are live accounts on this install are never included.
func previousIDs(
	ctx context.Context, db *sql.DB, tables []string, userID, username string, accounts AccountLookup,
) ([]string, error) {
	var candidates []string
	rows, err := db.QueryContext(ctx,
		`SELECT user_id FROM library_members WHERE username = ? COLLATE NOCASE AND user_id != ?`,
		username, userID)
	if err != nil {
		return nil, fmt.Errorf("reconnect member: look up username: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		candidates = append(candidates, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	if accounts.SoleAccount && len(candidates) == 0 {
		unknown, err := unrecordedIDs(ctx, db, tables, userID)
		if err != nil {
			return nil, err
		}
		if len(unknown) == 1 {
			candidates = unknown
		}
	}

	var out []string
	for _, id := range candidates {
		if accounts.Exists != nil {
			live, err := accounts.Exists(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("reconnect member: check account: %w", err)
			}
			if live {
				continue
			}
		}
		out = append(out, id)
	}
	return out, nil
}

// unrecordedIDs lists user_ids that own data in the library but have no
// library_members row: data written before members were recorded.
func unrecordedIDs(ctx context.Context, db *sql.DB, tables []string, userID string) ([]string, error) {
	if len(tables) == 0 {
		return nil, nil
	}
	parts := make([]string, 0, len(tables))
	for _, t := range tables {
		parts = append(parts, fmt.Sprintf(`SELECT user_id FROM %s`, quoteIdent(t)))
	}
	query := `SELECT DISTINCT user_id FROM (` + strings.Join(parts, ` UNION `) + `)
		WHERE user_id != '' AND user_id != ?
		  AND user_id NOT IN (SELECT user_id FROM library_members)`
	rows, err := db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("reconnect member: find unrecorded ids: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// userScopedTables lists every table holding per-user rows — any table with
// a user_id column, other than library_members itself — so a table added
// later is reconnected without anyone remembering to list it here.
func userScopedTables(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT m.name FROM sqlite_master m, pragma_table_info(m.name) c
		WHERE m.type = 'table' AND c.name = 'user_id' AND m.name != 'library_members'
		ORDER BY m.name`)
	if err != nil {
		return nil, fmt.Errorf("list user-scoped tables: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// GetMemberSetting decodes a member's setting into out, reporting whether it
// was set.
func GetMemberSetting(ctx context.Context, db *sql.DB, userID, key string, out any) (bool, error) {
	var raw string
	err := db.QueryRowContext(ctx,
		`SELECT value FROM member_settings WHERE user_id = ? AND key = ?`, userID, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get member setting: %w", err)
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return false, fmt.Errorf("decode member setting %q: %w", key, err)
	}
	return true, nil
}

// SetMemberSetting stores a member's setting as JSON.
func SetMemberSetting(ctx context.Context, db *sql.DB, userID, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode member setting %q: %w", key, err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO member_settings (user_id, key, value, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		userID, key, string(raw), time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("set member setting: %w", err)
	}
	return nil
}
