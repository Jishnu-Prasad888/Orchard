package memories

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// BlockType distinguishes text blocks from image blocks.
type BlockType string

const (
	BlockText  BlockType = "text"
	BlockImage BlockType = "image"
)

// Layout is how an image block arranges its images. The renderers live in the
// clients; the server only validates the name.
type Layout string

const (
	LayoutGrid      Layout = "grid"
	LayoutHero      Layout = "hero"
	LayoutMasonry   Layout = "masonry"
	LayoutTwoColumn Layout = "two_column"
	LayoutFilmstrip Layout = "filmstrip"
	LayoutFeatured  Layout = "featured"
)

// DefaultLayout is used when an image block names no layout.
const DefaultLayout = LayoutGrid

// IsKnownLayout reports whether l is a supported layout.
func IsKnownLayout(l Layout) bool {
	switch l {
	case LayoutGrid, LayoutHero, LayoutMasonry, LayoutTwoColumn, LayoutFilmstrip, LayoutFeatured:
		return true
	}
	return false
}

// Slideshow interval bounds, in seconds. A nil interval inherits the
// viewer's "Memory slideshow interval" setting (default 15 s).
const (
	MinSlideshowInterval = 3
	MaxSlideshowInterval = 3600
)

// Document limits. They exist to bound abuse, not to cap storytelling: a
// memory can hold thousands of blocks and each block megabytes of Markdown.
const (
	maxBlocks         = 5000
	maxImagesPerBlock = 500
	maxMarkdownBytes  = 2 << 20
	maxCaptionBytes   = 2000
)

// Block is one block of a memory. Markdown is set for text blocks; Layout,
// Slideshow, SlideshowInterval and Images for image blocks.
type Block struct {
	ID                string
	Type              BlockType
	Position          int
	Markdown          string
	Layout            Layout
	Slideshow         bool
	SlideshowInterval *int
	Images            []*Image
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Image is a memory's reference to a library file (a MemoryImage). Caption
// and Edits belong to this reference, never to the source media, so the same
// photo can appear in many memories with different captions and crops.
type Image struct {
	ID            string
	BlockID       string
	Position      int
	SourceFileID  string
	SourceRelPath string
	SourceHash    string
	Caption       string
	Edits         Edits
	DerivedID     string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// validateBlocks checks a document's shape before anything is written.
func validateBlocks(blocks []*Block) error {
	if len(blocks) > maxBlocks {
		return &ValidationError{msg: fmt.Sprintf("a memory can hold at most %d blocks", maxBlocks)}
	}
	ids := map[string]struct{}{}
	claim := func(id, what string) error {
		if id == "" {
			return nil
		}
		if !idPattern.MatchString(id) {
			return &ValidationError{msg: fmt.Sprintf("invalid %s id %q", what, id)}
		}
		if _, dup := ids[id]; dup {
			return &ValidationError{msg: fmt.Sprintf("duplicate id %q", id)}
		}
		ids[id] = struct{}{}
		return nil
	}
	for _, b := range blocks {
		if b == nil {
			return &ValidationError{msg: "block must not be null"}
		}
		if err := claim(b.ID, "block"); err != nil {
			return err
		}
		switch b.Type {
		case BlockText:
			if len(b.Markdown) > maxMarkdownBytes {
				return &ValidationError{msg: "text block is too large (max 2 MiB)"}
			}
			if len(b.Images) > 0 {
				return &ValidationError{msg: "text blocks cannot contain images"}
			}
			b.Layout, b.Slideshow, b.SlideshowInterval = "", false, nil
		case BlockImage:
			if b.Layout == "" {
				b.Layout = DefaultLayout
			}
			if !IsKnownLayout(b.Layout) {
				return &ValidationError{msg: fmt.Sprintf("unknown layout %q", b.Layout)}
			}
			if iv := b.SlideshowInterval; iv != nil && (*iv < MinSlideshowInterval || *iv > MaxSlideshowInterval) {
				return &ValidationError{msg: fmt.Sprintf("slideshow interval must be between %d and %d seconds",
					MinSlideshowInterval, MaxSlideshowInterval)}
			}
			if len(b.Images) > maxImagesPerBlock {
				return &ValidationError{msg: fmt.Sprintf("an image block can hold at most %d images", maxImagesPerBlock)}
			}
			b.Markdown = ""
			for _, img := range b.Images {
				if img == nil || strings.TrimSpace(img.SourceFileID) == "" {
					return &ValidationError{msg: "every image needs a file_id"}
				}
				if err := claim(img.ID, "image"); err != nil {
					return err
				}
				if len(img.Caption) > maxCaptionBytes {
					return &ValidationError{msg: "caption is too long (max 2000 characters)"}
				}
				if err := img.Edits.Validate(); err != nil {
					return err
				}
			}
		default:
			return &ValidationError{msg: fmt.Sprintf("unknown block type %q", b.Type)}
		}
	}
	return nil
}

// SaveDocument replaces a memory's blocks with blocks, in order, in one
// transaction. Blocks and images keep their IDs (clients may mint them), so
// unchanged content is updated in place and per-image derived copies survive
// unless their edits change. baseRevision, when non-nil, must equal the
// current revision or a *ConflictError is returned and nothing is written.
func (s *MemoryStore) SaveDocument(ctx context.Context, id string, baseRevision *int, blocks []*Block) (*Memory, error) {
	if err := validateBlocks(blocks); err != nil {
		return nil, err
	}
	now := rfc3339(s.now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin save document: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := lockMemory(ctx, tx, id, baseRevision); err != nil {
		return nil, err
	}
	if err := writeBlocksTx(ctx, tx, id, blocks, now); err != nil {
		return nil, err
	}
	if _, err := s.finalizeTx(ctx, tx, id, now, true); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit save document: %w", err)
	}
	return s.GetDocument(ctx, id)
}

// writeBlocksTx makes the stored blocks/images of memoryID equal blocks.
// IDs owned by a different memory are rejected rather than stolen.
func writeBlocksTx(ctx context.Context, tx *sql.Tx, memoryID string, blocks []*Block, now string) error {
	existing, err := loadBlocks(ctx, tx, memoryID)
	if err != nil {
		return err
	}
	oldImages := map[string]*Image{}
	oldBlocks := map[string]*Block{}
	for _, b := range existing {
		oldBlocks[b.ID] = b
		for _, img := range b.Images {
			oldImages[img.ID] = img
		}
	}

	var lookup []string
	for _, b := range blocks {
		for _, img := range b.Images {
			lookup = append(lookup, img.SourceFileID)
		}
	}
	sources, err := lookupSources(ctx, tx, lookup)
	if err != nil {
		return err
	}

	keepBlocks := map[string]struct{}{}
	keepImages := map[string]struct{}{}
	for pos, b := range blocks {
		if b.ID == "" {
			b.ID = newID()
		}
		b.Position = pos
		keepBlocks[b.ID] = struct{}{}
		if old, ok := oldBlocks[b.ID]; ok && sameBlock(old, b) {
			continue // unchanged: no write, timestamps stay put
		}
		var interval any
		if b.SlideshowInterval != nil {
			interval = *b.SlideshowInterval
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO memory_blocks (id, memory_id, position, type, markdown, layout, slideshow,
				slideshow_interval, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(id) DO UPDATE SET position = excluded.position, type = excluded.type,
				markdown = excluded.markdown, layout = excluded.layout, slideshow = excluded.slideshow,
				slideshow_interval = excluded.slideshow_interval, updated_at = excluded.updated_at
			 WHERE memory_blocks.memory_id = excluded.memory_id`,
			b.ID, memoryID, pos, string(b.Type), b.Markdown, string(b.Layout), boolInt(b.Slideshow),
			interval, now, now)
		if err != nil {
			return fmt.Errorf("write block: %w", err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return &ValidationError{msg: fmt.Sprintf("block id %q is already in use", b.ID)}
		}
	}

	// Delete removed blocks first: their images cascade, which frees image IDs
	// that may legitimately move to another block of this memory.
	for id := range oldBlocks {
		if _, keep := keepBlocks[id]; !keep {
			if _, err := tx.ExecContext(ctx, `DELETE FROM memory_blocks WHERE id = ? AND memory_id = ?`, id, memoryID); err != nil {
				return fmt.Errorf("delete block: %w", err)
			}
		}
	}

	for _, b := range blocks {
		for pos, img := range b.Images {
			if img.ID == "" {
				img.ID = newID()
			}
			img.BlockID = b.ID
			img.Position = pos
			keepImages[img.ID] = struct{}{}

			// An unchanged reference keeps its stored source details even if
			// the original has since gone missing; a new or replaced one must
			// resolve to an indexed photo or video.
			old, had := oldImages[img.ID]
			src := sources[img.SourceFileID]
			if had && old.SourceFileID == img.SourceFileID {
				img.SourceRelPath, img.SourceHash = old.SourceRelPath, old.SourceHash
			} else {
				if src == nil {
					return &ValidationError{msg: fmt.Sprintf("media %q not found in this library", img.SourceFileID)}
				}
				if src.MediaType != "photo" && src.MediaType != "video" {
					return &ValidationError{msg: fmt.Sprintf("%q is not a photo or video", src.RelPath)}
				}
				img.SourceRelPath, img.SourceHash = src.RelPath, src.ContentHash
			}
			if src != nil && src.MediaType == "video" && !img.Edits.IsDefault() {
				return &ValidationError{msg: "videos cannot be cropped, rotated or filtered"}
			}
			// A derived copy stays attached only while it still matches.
			derived := ""
			if had && old.DerivedID != "" &&
				old.Edits.Signature(old.SourceFileID) == img.Edits.Signature(img.SourceFileID) {
				derived = old.DerivedID
			}
			img.DerivedID = derived
			if had && sameImage(old, img) {
				continue
			}

			var cx, cy, cw, ch any
			if c := img.Edits.Crop; c != nil {
				cx, cy, cw, ch = c.X, c.Y, c.Width, c.Height
			}
			res, err := tx.ExecContext(ctx,
				`INSERT INTO memory_images (id, memory_id, block_id, position, source_file_id, source_rel_path,
					source_hash, caption, crop_x, crop_y, crop_w, crop_h, rotation, filter, brightness,
					contrast, saturation, derived_id, created_at, updated_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				 ON CONFLICT(id) DO UPDATE SET block_id = excluded.block_id, position = excluded.position,
					source_file_id = excluded.source_file_id, source_rel_path = excluded.source_rel_path,
					source_hash = excluded.source_hash, caption = excluded.caption,
					crop_x = excluded.crop_x, crop_y = excluded.crop_y, crop_w = excluded.crop_w,
					crop_h = excluded.crop_h, rotation = excluded.rotation, filter = excluded.filter,
					brightness = excluded.brightness, contrast = excluded.contrast,
					saturation = excluded.saturation, derived_id = excluded.derived_id,
					updated_at = excluded.updated_at
				 WHERE memory_images.memory_id = excluded.memory_id`,
				img.ID, memoryID, b.ID, pos, img.SourceFileID, img.SourceRelPath, img.SourceHash,
				img.Caption, cx, cy, cw, ch, img.Edits.Rotation, img.Edits.Filter,
				img.Edits.Adjustments.Brightness, img.Edits.Adjustments.Contrast,
				img.Edits.Adjustments.Saturation, nullIfEmpty(derived), now, now)
			if err != nil {
				return fmt.Errorf("write image: %w", err)
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return &ValidationError{msg: fmt.Sprintf("image id %q is already in use", img.ID)}
			}
		}
	}

	for id := range oldImages {
		if _, keep := keepImages[id]; !keep {
			if _, err := tx.ExecContext(ctx, `DELETE FROM memory_images WHERE id = ? AND memory_id = ?`, id, memoryID); err != nil {
				return fmt.Errorf("delete image: %w", err)
			}
		}
	}
	return nil
}

func sameBlock(a, b *Block) bool {
	return a.Position == b.Position && a.Type == b.Type && a.Markdown == b.Markdown &&
		a.Layout == b.Layout && a.Slideshow == b.Slideshow && sameIntPtr(a.SlideshowInterval, b.SlideshowInterval)
}

func sameImage(a, b *Image) bool {
	return a.BlockID == b.BlockID && a.Position == b.Position && a.SourceFileID == b.SourceFileID &&
		a.Caption == b.Caption && a.DerivedID == b.DerivedID && sameEdits(a.Edits, b.Edits)
}

func sameEdits(a, b Edits) bool {
	if (a.Crop == nil) != (b.Crop == nil) || (a.Crop != nil && *a.Crop != *b.Crop) {
		return false
	}
	return a.Rotation == b.Rotation && a.Filter == b.Filter && a.Adjustments == b.Adjustments
}

func sameIntPtr(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// loadBlocks reads a memory's blocks and images in order.
func loadBlocks(ctx context.Context, q querier, memoryID string) ([]*Block, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT id, type, position, markdown, layout, slideshow, slideshow_interval, created_at, updated_at
		 FROM memory_blocks WHERE memory_id = ? ORDER BY position, id`, memoryID)
	if err != nil {
		return nil, fmt.Errorf("load blocks: %w", err)
	}
	var blocks []*Block
	byID := map[string]*Block{}
	for rows.Next() {
		var (
			b                Block
			typ, layout      string
			slideshow        int
			interval         sql.NullInt64
			created, updated string
		)
		if err := rows.Scan(&b.ID, &typ, &b.Position, &b.Markdown, &layout, &slideshow, &interval,
			&created, &updated); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan block: %w", err)
		}
		b.Type, b.Layout, b.Slideshow = BlockType(typ), Layout(layout), slideshow != 0
		if interval.Valid {
			v := int(interval.Int64)
			b.SlideshowInterval = &v
		}
		b.CreatedAt, b.UpdatedAt = parseOrZero(created), parseOrZero(updated)
		if b.Type == BlockImage {
			b.Images = []*Image{}
		}
		blocks = append(blocks, &b)
		byID[b.ID] = &b
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	irows, err := q.QueryContext(ctx,
		`SELECT id, block_id, position, source_file_id, source_rel_path, source_hash, caption,
			crop_x, crop_y, crop_w, crop_h, rotation, filter, brightness, contrast, saturation,
			COALESCE(derived_id, ''), created_at, updated_at
		 FROM memory_images WHERE memory_id = ? ORDER BY block_id, position, id`, memoryID)
	if err != nil {
		return nil, fmt.Errorf("load images: %w", err)
	}
	defer func() { _ = irows.Close() }()
	for irows.Next() {
		var (
			img              Image
			cx, cy, cw, ch   sql.NullFloat64
			created, updated string
		)
		if err := irows.Scan(&img.ID, &img.BlockID, &img.Position, &img.SourceFileID, &img.SourceRelPath,
			&img.SourceHash, &img.Caption, &cx, &cy, &cw, &ch, &img.Edits.Rotation, &img.Edits.Filter,
			&img.Edits.Adjustments.Brightness, &img.Edits.Adjustments.Contrast,
			&img.Edits.Adjustments.Saturation, &img.DerivedID, &created, &updated); err != nil {
			return nil, fmt.Errorf("scan image: %w", err)
		}
		if cx.Valid && cy.Valid && cw.Valid && ch.Valid {
			img.Edits.Crop = &Crop{X: cx.Float64, Y: cy.Float64, Width: cw.Float64, Height: ch.Float64}
		}
		img.CreatedAt, img.UpdatedAt = parseOrZero(created), parseOrZero(updated)
		if b, ok := byID[img.BlockID]; ok {
			b.Images = append(b.Images, &img)
		}
	}
	return blocks, irows.Err()
}

// --- version snapshots ---

type snapshotImage struct {
	ID          string      `json:"id"`
	FileID      string      `json:"file_id"`
	Caption     string      `json:"caption,omitempty"`
	Crop        *Crop       `json:"crop,omitempty"`
	Rotation    int         `json:"rotation,omitempty"`
	Filter      string      `json:"filter,omitempty"`
	Adjustments Adjustments `json:"adjustments"`
}

type snapshotBlock struct {
	ID                string          `json:"id"`
	Type              BlockType       `json:"type"`
	Markdown          string          `json:"markdown,omitempty"`
	Layout            Layout          `json:"layout,omitempty"`
	Slideshow         bool            `json:"slideshow,omitempty"`
	SlideshowInterval *int            `json:"slideshow_interval,omitempty"`
	Images            []snapshotImage `json:"images,omitempty"`
}

// encodeSnapshot serializes blocks for memory_versions.document. Snapshots
// hold edit parameters, not derived files: a restored version re-renders its
// copies, so garbage-collecting derived media never breaks history.
func encodeSnapshot(blocks []*Block) (string, error) {
	out := make([]snapshotBlock, 0, len(blocks))
	for _, b := range blocks {
		sb := snapshotBlock{ID: b.ID, Type: b.Type, Markdown: b.Markdown, Layout: b.Layout,
			Slideshow: b.Slideshow, SlideshowInterval: b.SlideshowInterval}
		for _, img := range b.Images {
			sb.Images = append(sb.Images, snapshotImage{ID: img.ID, FileID: img.SourceFileID,
				Caption: img.Caption, Crop: img.Edits.Crop, Rotation: img.Edits.Rotation,
				Filter: img.Edits.Filter, Adjustments: img.Edits.Adjustments})
		}
		out = append(out, sb)
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("encode memory snapshot: %w", err)
	}
	return string(raw), nil
}

// DecodeSnapshot parses a version's document back into blocks. An empty
// document (a pre-v8 version) becomes a single text block holding body.
func DecodeSnapshot(document, body string) ([]*Block, error) {
	if document == "" {
		return []*Block{{Type: BlockText, Markdown: body}}, nil
	}
	var in []snapshotBlock
	if err := json.Unmarshal([]byte(document), &in); err != nil {
		return nil, fmt.Errorf("decode memory snapshot: %w", err)
	}
	out := make([]*Block, 0, len(in))
	for pos, sb := range in {
		b := &Block{ID: sb.ID, Type: sb.Type, Position: pos, Markdown: sb.Markdown, Layout: sb.Layout,
			Slideshow: sb.Slideshow, SlideshowInterval: sb.SlideshowInterval}
		for ipos, si := range sb.Images {
			b.Images = append(b.Images, &Image{ID: si.ID, BlockID: sb.ID, Position: ipos,
				SourceFileID: si.FileID, Caption: si.Caption,
				Edits: Edits{Crop: si.Crop, Rotation: si.Rotation, Filter: si.Filter, Adjustments: si.Adjustments}})
		}
		out = append(out, b)
	}
	return out, nil
}

// --- source media ---

// Source is what the store knows about a referenced library file.
type Source struct {
	FileID      string
	RelPath     string
	ContentHash string
	Status      string // present, missing, deleted
	MediaType   string
	MIMEType    string
	Width       int
	Height      int
}

// Available reports whether the original can currently be shown.
func (s *Source) Available() bool { return s != nil && s.Status == "present" }

// LookupSources resolves file IDs against the library index. IDs that are
// not indexed are absent from the result.
func (s *MemoryStore) LookupSources(ctx context.Context, ids []string) (map[string]*Source, error) {
	return lookupSources(ctx, s.db, ids)
}

func lookupSources(ctx context.Context, q querier, ids []string) (map[string]*Source, error) {
	out := map[string]*Source{}
	uniq := dedupe(ids)
	for start := 0; start < len(uniq); start += 400 {
		end := min(start+400, len(uniq))
		chunk := uniq[start:end]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := q.QueryContext(ctx,
			`SELECT f.id, f.rel_path, COALESCE(f.content_hash, ''), f.status, f.media_type,
				COALESCE(m.mime_type, ''), COALESCE(m.width, 0), COALESCE(m.height, 0)
			 FROM indexed_files f LEFT JOIN media_metadata m ON m.file_id = f.id
			 WHERE f.id IN (`+placeholders(len(chunk))+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("lookup memory sources: %w", err)
		}
		for rows.Next() {
			var src Source
			if err := rows.Scan(&src.FileID, &src.RelPath, &src.ContentHash, &src.Status, &src.MediaType,
				&src.MIMEType, &src.Width, &src.Height); err != nil {
				_ = rows.Close()
				return nil, err
			}
			out[src.FileID] = &src
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return out, nil
}

// ReconcileSources re-attaches images whose original went missing to a
// present file with the same content hash (a moved or re-indexed original).
// It reports whether anything changed; a change bumps the revision so stale
// editors reload instead of writing the old reference back.
func (s *MemoryStore) ReconcileSources(ctx context.Context, memoryID string) (bool, error) {
	blocks, err := loadBlocks(ctx, s.db, memoryID)
	if err != nil {
		return false, err
	}
	var ids []string
	for _, b := range blocks {
		for _, img := range b.Images {
			ids = append(ids, img.SourceFileID)
		}
	}
	if len(ids) == 0 {
		return false, nil
	}
	sources, err := lookupSources(ctx, s.db, ids)
	if err != nil {
		return false, err
	}
	type move struct{ imageID, fileID, relPath string }
	var moves []move
	for _, b := range blocks {
		for _, img := range b.Images {
			if sources[img.SourceFileID].Available() || img.SourceHash == "" {
				continue
			}
			var fileID, relPath string
			err := s.db.QueryRowContext(ctx,
				`SELECT id, rel_path FROM indexed_files WHERE content_hash = ? AND status = 'present'
				 ORDER BY (rel_path = ?) DESC, rel_path LIMIT 1`, img.SourceHash, img.SourceRelPath).
				Scan(&fileID, &relPath)
			if err == sql.ErrNoRows {
				continue
			}
			if err != nil {
				return false, err
			}
			moves = append(moves, move{img.ID, fileID, relPath})
		}
	}
	if len(moves) == 0 {
		return false, nil
	}
	now := rfc3339(s.now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := lockMemory(ctx, tx, memoryID, nil); err != nil {
		return false, err
	}
	for _, m := range moves {
		// The derived copy was rendered from the old file id; drop it so it
		// is regenerated (its bytes would be identical, but its signature
		// names the source).
		if _, err := tx.ExecContext(ctx,
			`UPDATE memory_images SET source_file_id = ?, source_rel_path = ?, derived_id = NULL, updated_at = ?
			 WHERE id = ? AND memory_id = ?`, m.fileID, m.relPath, now, m.imageID, memoryID); err != nil {
			return false, err
		}
	}
	if _, err := s.finalizeTx(ctx, tx, memoryID, now, true); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// --- helpers ---

func dedupe(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok || id == "" {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func parseOrZero(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}
