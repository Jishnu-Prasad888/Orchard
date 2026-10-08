package librarydb

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
)

// memoryColumns are the columns schema v8 adds to tables that predate it.
// SQLite has no ADD COLUMN IF NOT EXISTS, so each is added only when missing.
var memoryColumns = []struct {
	table, column, ddl string
}{
	{"memories", "description", `ALTER TABLE memories ADD COLUMN description TEXT NOT NULL DEFAULT ''`},
	{"memories", "location", `ALTER TABLE memories ADD COLUMN location TEXT NOT NULL DEFAULT ''`},
	{"memories", "cover_file_id", `ALTER TABLE memories ADD COLUMN cover_file_id TEXT`},
	{"memories", "revision", `ALTER TABLE memories ADD COLUMN revision INTEGER NOT NULL DEFAULT 1`},
	{"memories", "search_text", `ALTER TABLE memories ADD COLUMN search_text TEXT NOT NULL DEFAULT ''`},
	{"memories", "blocks_migrated", `ALTER TABLE memories ADD COLUMN blocks_migrated INTEGER NOT NULL DEFAULT 0`},
	{"memory_versions", "document", `ALTER TABLE memory_versions ADD COLUMN document TEXT`},
}

// ftsMemoryTriggers replace the v7 triggers so the FTS index covers
// search_text (Markdown of every text block, image captions, description,
// location and tags) rather than the legacy single body.
const ftsMemoryTriggers = `
DROP TRIGGER IF EXISTS fts_memories_insert;
DROP TRIGGER IF EXISTS fts_memories_update;
CREATE TRIGGER fts_memories_insert AFTER INSERT ON memories BEGIN
	INSERT INTO fts_memories(memory_id, title, body) VALUES (new.id, new.title, new.search_text);
END;
CREATE TRIGGER fts_memories_update AFTER UPDATE OF title, body, deleted, search_text ON memories BEGIN
	DELETE FROM fts_memories WHERE memory_id = old.id;
	INSERT INTO fts_memories(memory_id, title, body) VALUES (new.id, new.title, new.search_text);
END;
`

// upgradeMemories brings a library database to the block-based memory model
// (schema v8). It runs on every open, so each step is guarded and cheap once
// applied:
//
//  1. add the new memories / memory_versions columns when missing;
//  2. point the FTS triggers at search_text (once);
//  3. convert every legacy memory's Markdown body into a single text block.
//
// Step 3 is the backward-compatibility migration: nothing is deleted, the
// body column keeps its content, timestamps are untouched, and [[type:id]]
// links stay in the Markdown exactly as written.
func upgradeMemories(db *sql.DB) error {
	for _, c := range memoryColumns {
		has, err := columnExists(db, c.table, c.column)
		if err != nil {
			return err
		}
		if !has {
			if _, err := db.Exec(c.ddl); err != nil {
				return fmt.Errorf("add %s.%s: %w", c.table, c.column, err)
			}
		}
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS memories_unmigrated_idx
		ON memories (id) WHERE blocks_migrated = 0`); err != nil {
		return err
	}

	var triggerSQL string
	err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = 'fts_memories_insert'`).
		Scan(&triggerSQL)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if !strings.Contains(triggerSQL, "search_text") {
		if _, err := db.Exec(ftsMemoryTriggers); err != nil {
			return fmt.Errorf("replace memory fts triggers: %w", err)
		}
	}

	return migrateLegacyMemoryBodies(db)
}

// migrateLegacyMemoryBodies wraps each pre-v8 memory's body in one text block.
func migrateLegacyMemoryBodies(db *sql.DB) error {
	rows, err := db.Query(`SELECT id, body, created_at, updated_at FROM memories WHERE blocks_migrated = 0`)
	if err != nil {
		return err
	}
	type legacy struct{ id, body, created, updated string }
	var pending []legacy
	for rows.Next() {
		var l legacy
		if err := rows.Scan(&l.id, &l.body, &l.created, &l.updated); err != nil {
			_ = rows.Close()
			return err
		}
		pending = append(pending, l)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	for _, l := range pending {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		var blocks int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM memory_blocks WHERE memory_id = ?`, l.id).Scan(&blocks); err != nil {
			_ = tx.Rollback()
			return err
		}
		if blocks == 0 {
			if _, err := tx.Exec(`INSERT INTO memory_blocks (id, memory_id, position, type, markdown, created_at, updated_at)
				VALUES (?, ?, 0, 'text', ?, ?, ?)`, randomID(), l.id, l.body, l.created, l.updated); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("migrate memory %s: %w", l.id, err)
			}
		}
		if _, err := tx.Exec(`UPDATE memories SET search_text = body, blocks_migrated = 1 WHERE id = ?`, l.id); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func randomID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand: %v", err))
	}
	return hex.EncodeToString(b)
}
