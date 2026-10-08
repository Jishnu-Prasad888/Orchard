package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// cairnDirName is the metadata directory name that must never be indexed.
const cairnDirName = ".cairn"

// Scanner walks a library root and reconciles the filesystem against the
// persisted index. It implements staged identity detection to avoid
// computing SHA-256 content hashes unless a file's size or modification time
// indicates it may have changed.
type Scanner struct {
	store  *StateStore
	logger *slog.Logger
}

// NewScanner constructs a Scanner backed by the given per-library StateStore.
func NewScanner(store *StateStore, logger *slog.Logger) *Scanner {
	return &Scanner{store: store, logger: logger}
}

// Scan performs a full recursive scan of root and reconciles it against the
// persisted index. The context is checked for cancellation; a cancelled scan
// returns a partial result with any changes made up to that point.
func (sc *Scanner) Scan(ctx context.Context, libraryID, root string) (*Result, error) {
	now := time.Now().UTC()
	result := &Result{
		LibraryID: libraryID,
		StartedAt: now,
	}

	// Load all currently-indexed files into a map keyed by relative path so
	// we can detect moves and missing entries in O(1) per file.
	existingByPath, err := sc.loadExisting(ctx)
	if err != nil {
		return nil, fmt.Errorf("load existing index: %w", err)
	}

	// Build a secondary hash → []*IndexedFile index for move detection.
	// Only entries that have a content hash are useful here.
	existingByHash := make(map[string][]*IndexedFile)
	for _, f := range existingByPath {
		if f.ContentHash != "" {
			existingByHash[f.ContentHash] = append(existingByHash[f.ContentHash], f)
		}
	}

	// seenIDs tracks which existing records were observed in this scan so we
	// can mark unvisited records as missing afterwards.
	seenIDs := make(map[string]struct{})

	walkErr := filepath.WalkDir(root, func(absPath string, d os.DirEntry, walkInErr error) error {
		if err := ctx.Err(); err != nil {
			return err // propagate cancellation
		}
		if walkInErr != nil {
			sc.logger.Warn("walk error", "path", absPath, "error", walkInErr)
			result.Errors++
			return nil // continue walking; don't abort on inaccessible files
		}

		// Skip the metadata directory entirely.
		if d.IsDir() && d.Name() == cairnDirName {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil // descend into ordinary directories
		}

		relPath, err := filepath.Rel(root, absPath)
		if err != nil {
			sc.logger.Warn("relative path error", "abs", absPath, "error", err)
			result.Errors++
			return nil
		}
		// Normalise to forward slashes for portability.
		relPath = filepath.ToSlash(relPath)

		info, err := d.Info()
		if err != nil {
			sc.logger.Warn("stat error", "path", absPath, "error", err)
			result.Errors++
			return nil
		}

		change, err := sc.reconcileFile(ctx, relPath, absPath, info, now,
			existingByPath, existingByHash, seenIDs)
		if err != nil {
			sc.logger.Warn("reconcile error", "path", relPath, "error", err)
			result.Errors++
			return nil
		}
		result.TotalFiles++
		result.Changes = append(result.Changes, change)
		switch change.Kind {
		case ChangeNew:
			result.NewFiles++
		case ChangeModified:
			result.Modified++
		case ChangeMoved:
			result.Moved++
		case ChangeUnchanged:
			result.Unchanged++
		}
		return nil
	})

	if walkErr != nil && !isContextErr(walkErr) {
		return result, fmt.Errorf("walk %s: %w", root, walkErr)
	}

	// Mark files not seen in this scan as missing.
	missingCount, err := sc.store.MarkMissing(ctx, seenIDs, now)
	if err != nil && !isContextErr(err) {
		return result, fmt.Errorf("mark missing: %w", err)
	}
	result.Missing = missingCount

	result.FinishedAt = time.Now().UTC()
	sc.logger.Info("scan complete",
		"library_id", libraryID,
		"total", result.TotalFiles,
		"new", result.NewFiles,
		"modified", result.Modified,
		"moved", result.Moved,
		"missing", result.Missing,
		"errors", result.Errors,
		"duration", result.FinishedAt.Sub(result.StartedAt),
	)
	return result, nil
}

// reconcileFile determines what happened to one file and persists the update.
func (sc *Scanner) reconcileFile(
	ctx context.Context,
	relPath, absPath string,
	info os.FileInfo,
	now time.Time,
	existingByPath map[string]*IndexedFile,
	existingByHash map[string][]*IndexedFile,
	seenIDs map[string]struct{},
) (FileChange, error) {
	existing, known := existingByPath[relPath]

	// Stage 1: unchanged — same path, size, and mtime.
	if known && existing.SizeBytes == info.Size() && existing.ModTime.Equal(info.ModTime().UTC()) {
		seenIDs[existing.ID] = struct{}{}
		existing.LastSeenAt = now
		existing.Status = StatusPresent
		if err := sc.store.Upsert(ctx, existing); err != nil {
			return FileChange{}, err
		}
		return FileChange{Kind: ChangeUnchanged, File: existing}, nil
	}

	// Stage 2: size or mtime changed — compute the content hash to distinguish
	// a genuine modification from a metadata-only change (e.g. touch).
	hash, err := hashFile(absPath)
	if err != nil {
		return FileChange{}, fmt.Errorf("hash %s: %w", absPath, err)
	}

	if known {
		// Same path but different content: modified file.
		existing.SizeBytes = info.Size()
		existing.ModTime = info.ModTime().UTC()
		existing.ContentHash = hash
		existing.Status = StatusPresent
		existing.LastSeenAt = now
		existing.IndexedAt = now
		seenIDs[existing.ID] = struct{}{}
		if err := sc.store.Upsert(ctx, existing); err != nil {
			return FileChange{}, err
		}
		return FileChange{Kind: ChangeModified, File: existing}, nil
	}

	// Stage 3 & 4: new path — check if any existing entry with the same hash
	// has not been seen in this scan yet (move detection). We accept both
	// "missing" (previously undetected move) and "present" entries that are not
	// yet in seenIDs, which handles files moved within the same scan pass.
	if candidates, ok := existingByHash[hash]; ok {
		for _, candidate := range candidates {
			_, alreadySeen := seenIDs[candidate.ID]
			if alreadySeen {
				continue // this file was already accounted for at its own path
			}
			if candidate.Status == StatusPresent || candidate.Status == StatusMissing {
				// This looks like a move: same content, old entry not yet seen.
				oldPath := candidate.RelPath
				if err := sc.store.UpdatePath(ctx, candidate.ID, relPath, now); err != nil {
					return FileChange{}, err
				}
				candidate.RelPath = relPath
				candidate.SizeBytes = info.Size()
				candidate.ModTime = info.ModTime().UTC()
				candidate.ContentHash = hash
				candidate.Status = StatusPresent
				candidate.LastSeenAt = now
				candidate.IndexedAt = now
				seenIDs[candidate.ID] = struct{}{}
				// Remove the old path from the map so MarkMissing won't re-mark it.
				delete(existingByPath, oldPath)
				existingByPath[relPath] = candidate
				return FileChange{Kind: ChangeMoved, File: candidate, OldPath: oldPath}, nil
			}
		}
	}

	// Stage 5: genuinely new file.
	f := &IndexedFile{
		ID:          newID(),
		RelPath:     relPath,
		SizeBytes:   info.Size(),
		ModTime:     info.ModTime().UTC(),
		ContentHash: hash,
		Status:      StatusPresent,
		FirstSeenAt: now,
		LastSeenAt:  now,
		IndexedAt:   now,
	}
	seenIDs[f.ID] = struct{}{}
	if err := sc.store.Upsert(ctx, f); err != nil {
		return FileChange{}, err
	}
	existingByPath[relPath] = f
	existingByHash[hash] = append(existingByHash[hash], f)
	return FileChange{Kind: ChangeNew, File: f}, nil
}

// loadExisting fetches all present/missing files into a path-keyed map.
func (sc *Scanner) loadExisting(ctx context.Context) (map[string]*IndexedFile, error) {
	files, err := sc.store.AllPresent(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]*IndexedFile, len(files))
	for _, f := range files {
		m[f.RelPath] = f
	}
	return m, nil
}

// hashFile computes the SHA-256 of a file without loading it into memory.
// It reads in 32 KiB chunks, keeping peak memory constant regardless of file
// size. This is safe to call on large video files.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open for hashing: %w", err)
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	buf := make([]byte, 32*1024)
	if _, err := io.CopyBuffer(h, f, buf); err != nil {
		return "", fmt.Errorf("hash read: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// isContextErr returns true when the error is a context cancellation or
// deadline. Used to distinguish expected scan termination from real errors.
func isContextErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "context canceled") ||
		strings.Contains(s, "context deadline exceeded")
}
