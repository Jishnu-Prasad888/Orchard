package ml

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// FaceRecord is one stored face observation for a file.
type FaceRecord struct {
	ID         string
	FileID     string
	Provider   string
	Version    int
	Box        FaceBox
	Descriptor []float32
	CreatedAt  time.Time
	UpdatedAt  time.Time
	// RelPath is populated only by queries that join indexed_files (used to
	// re-open the source image when serving face crops).
	RelPath string
}

// Person is one nameable grouping of faces. Names survive face purges.
type Person struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	CoverFaceID string    `json:"cover_face_id,omitempty"`
	CoverFileID string    `json:"cover_file_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	FaceCount   int       `json:"face_count"`
}

// UnsignedFaceFile is a photo that has no faces recorded for a
// provider/version yet.
type UnsignedFaceFile struct {
	FileID  string
	RelPath string
}

// FaceStore persists faces, people, and face-person assignments in the
// per-library database. Like the signature store, it is derived data: the
// whole table may be purged and regenerated, while people (user-curated
// names) survive.
type FaceStore struct {
	db *sql.DB
}

// NewFaceStore wraps a per-library database pool.
func NewFaceStore(db *sql.DB) *FaceStore {
	return &FaceStore{db: db}
}

// FilesToScan returns present photos with no faces recorded for the given
// provider version yet. The photo test uses the type the scanner recorded on
// the file itself: the post-scan hook runs before media-metadata jobs have
// filled media_metadata, so depending on it would find nothing on first scan.
func (s *FaceStore) FilesToScan(ctx context.Context, provider string, version int) ([]UnsignedFaceFile, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.id, f.rel_path
		FROM indexed_files f
		WHERE f.status = 'present'
		  AND f.media_type = 'photo'
		  AND NOT EXISTS (
		      SELECT 1 FROM face_scans fs
		      WHERE fs.file_id = f.id AND fs.provider = ? AND fs.version = ?
		  )
		  AND NOT EXISTS (
		      SELECT 1 FROM faces fa
		      WHERE fa.file_id = f.id AND fa.provider = ? AND fa.version = ?
		  )
		ORDER BY f.id`, provider, version, provider, version)
	if err != nil {
		return nil, fmt.Errorf("list files to face-scan: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var files []UnsignedFaceFile
	for rows.Next() {
		var uf UnsignedFaceFile
		if err := rows.Scan(&uf.FileID, &uf.RelPath); err != nil {
			return nil, fmt.Errorf("scan file to face-scan: %w", err)
		}
		files = append(files, uf)
	}
	return files, rows.Err()
}

// InsertFace writes one face observation.
func (s *FaceStore) InsertFace(ctx context.Context, f FaceRecord) error {
	now := rfc3339(time.Now().UTC())
	c, err := float32Bytes(f.Descriptor)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO faces (id, file_id, provider, version, x, y, width, height,
		                   confidence, descriptor, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.ID, f.FileID, f.Provider, f.Version,
		f.Box.X, f.Box.Y, f.Box.Width, f.Box.Height, f.Box.Confidence,
		c, now, now)
	if err != nil {
		return fmt.Errorf("insert face: %w", err)
	}
	return nil
}

// RecordFaceScan stores the faces found in one file and marks the file as
// scanned by provider/version, atomically: a file is either fully recorded or
// not at all, so an interrupted pass never leaves half of a photo's faces.
func (s *FaceStore) RecordFaceScan(ctx context.Context, fileID, provider string, version int, faces []FaceRecord) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("record face scan begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := rfc3339(time.Now().UTC())
	for _, f := range faces {
		c, err := float32Bytes(f.Descriptor)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO faces (id, file_id, provider, version, x, y, width, height,
			                   confidence, descriptor, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			f.ID, f.FileID, f.Provider, f.Version,
			f.Box.X, f.Box.Y, f.Box.Width, f.Box.Height, f.Box.Confidence,
			c, now, now); err != nil {
			return fmt.Errorf("insert face: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO face_scans (file_id, provider, version, faces, scanned_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(file_id) DO UPDATE SET provider = excluded.provider,
		    version = excluded.version, faces = excluded.faces, scanned_at = excluded.scanned_at`,
		fileID, provider, version, len(faces), now); err != nil {
		return fmt.Errorf("record face scan: %w", err)
	}
	return tx.Commit()
}

// DedupeFaces removes repeated observations of one face (same photo, provider,
// version and box), keeping a grouped copy when there is one. Overlapping
// passes used to store faces twice. Returns the number removed.
func (s *FaceStore) DedupeFaces(ctx context.Context) (int, error) {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM faces WHERE id IN (
			SELECT fa.id FROM faces fa
			WHERE NOT EXISTS (SELECT 1 FROM person_faces p WHERE p.face_id = fa.id)
			  AND EXISTS (
				SELECT 1 FROM faces o
				WHERE o.file_id = fa.file_id AND o.provider = fa.provider
				  AND o.version = fa.version AND o.x = fa.x AND o.y = fa.y
				  AND o.width = fa.width AND o.height = fa.height
				  AND o.id <> fa.id
				  AND (o.rowid < fa.rowid
				       OR EXISTS (SELECT 1 FROM person_faces p WHERE p.face_id = o.id))
			  )
		)`)
	if err != nil {
		return 0, fmt.Errorf("dedupe faces: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// FaceByID returns a face, or nil when not present.
func (s *FaceStore) FaceByID(ctx context.Context, id string) (*FaceRecord, error) {
	var (
		f                            FaceRecord
		descriptor, created, updated string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, file_id, provider, version, x, y, width, height, confidence,
		       descriptor, created_at, updated_at
		FROM faces WHERE id = ?`, id).
		Scan(&f.ID, &f.FileID, &f.Provider, &f.Version,
			&f.Box.X, &f.Box.Y, &f.Box.Width, &f.Box.Height, &f.Box.Confidence,
			&descriptor, &created, &updated)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get face: %w", err)
	}
	desc, err := float32FromBytes([]byte(descriptor))
	if err != nil {
		return nil, err
	}
	f.Descriptor = desc
	if err := parseTime(created, &f.CreatedAt); err != nil {
		return nil, err
	}
	if err := parseTime(updated, &f.UpdatedAt); err != nil {
		return nil, err
	}
	return &f, nil
}

// FacesUnassigned returns faces that belong to no person, oldest first, with
// their image's relative path (used to render crops).
func (s *FaceStore) FacesUnassigned(ctx context.Context) ([]FaceRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT fa.id, fa.file_id, fa.provider, fa.version, fa.x, fa.y, fa.width,
		       fa.height, fa.confidence, fa.descriptor, fa.created_at, fa.updated_at,
		       f.rel_path
		FROM faces fa
		JOIN indexed_files f ON f.id = fa.file_id
		WHERE NOT EXISTS (SELECT 1 FROM person_faces pf WHERE pf.face_id = fa.id)
		ORDER BY fa.created_at, fa.id`)
	if err != nil {
		return nil, fmt.Errorf("list unassigned faces: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []FaceRecord
	for rows.Next() {
		f, err := scanFaceFull(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// PersonMeans returns each person's mean (normalized) descriptor over all
// their assigned faces. People with no faces are absent from the map.
func (s *FaceStore) PersonMeans(ctx context.Context) (map[string][]float32, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT pf.person_id, fa.descriptor
		FROM person_faces pf
		JOIN faces fa ON fa.id = pf.face_id`)
	if err != nil {
		return nil, fmt.Errorf("list person faces: %w", err)
	}
	defer func() { _ = rows.Close() }()

	sums := map[string][]float64{}
	counts := map[string]int{}
	var dim int
	for rows.Next() {
		var pid string
		var blob []byte
		if err := rows.Scan(&pid, &blob); err != nil {
			return nil, fmt.Errorf("scan person face: %w", err)
		}
		desc, err := float32FromBytes(blob)
		if err != nil {
			return nil, err
		}
		if dim == 0 {
			dim = len(desc)
		}
		sum := sums[pid]
		if sum == nil {
			sum = make([]float64, dim)
			sums[pid] = sum
		}
		for i, v := range desc {
			sum[i] += float64(v)
		}
		counts[pid]++
	}

	means := make(map[string][]float32, len(sums))
	for pid, sum := range sums {
		out := make([]float32, len(sum))
		var n float64
		for i, v := range sum {
			av := v / float64(counts[pid])
			out[i] = float32(av)
			n += av * av
		}
		if n == 0 {
			continue // flat person: no mean to compare against
		}
		inv := 1.0 / math.Sqrt(n)
		for i := range out {
			out[i] = float32(float64(out[i]) * inv)
		}
		means[pid] = out
	}
	return means, nil
}

// CountFaces returns the number of stored face observations.
func (s *FaceStore) CountFaces(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM faces`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count faces: %w", err)
	}
	return n, nil
}

// CountPeople returns the number of people.
func (s *FaceStore) CountPeople(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM people`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count people: %w", err)
	}
	return n, nil
}

// ListPeople returns all people (sorted so the big, meaningful groups sort
// first is left to callers) with their face counts.
func (s *FaceStore) ListPeople(ctx context.Context) ([]Person, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.id, p.name, COALESCE(p.cover_face_id, ''), COALESCE(p.cover_file_id, ''),
		       p.created_at, p.updated_at,
		       (SELECT COUNT(*) FROM person_faces pf WHERE pf.person_id = p.id)
		FROM people p
		ORDER BY p.name COLLATE NOCASE, p.id`)
	if err != nil {
		return nil, fmt.Errorf("list people: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Person
	for rows.Next() {
		var (
			p                Person
			created, updated string
		)
		if err := rows.Scan(&p.ID, &p.Name, &p.CoverFaceID, &p.CoverFileID,
			&created, &updated, &p.FaceCount); err != nil {
			return nil, fmt.Errorf("scan person: %w", err)
		}
		if err := parseTime(created, &p.CreatedAt); err != nil {
			return nil, err
		}
		if err := parseTime(updated, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PersonFaces returns the faces assigned to a person (used to render covers
// and the person grid).
func (s *FaceStore) PersonFaces(ctx context.Context, personID string) ([]FaceRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT fa.id, fa.file_id, fa.provider, fa.version, fa.x, fa.y, fa.width,
		       fa.height, fa.confidence, fa.descriptor, fa.created_at, fa.updated_at,
		       f.rel_path
		FROM person_faces pf
		JOIN faces fa ON fa.id = pf.face_id
		JOIN indexed_files f ON f.id = fa.file_id
		WHERE pf.person_id = ?
		ORDER BY fa.created_at, fa.id`, personID)
	if err != nil {
		return nil, fmt.Errorf("list person faces: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []FaceRecord
	for rows.Next() {
		f, err := scanFaceFull(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// CreatePerson inserts a person and returns it.
func (s *FaceStore) CreatePerson(ctx context.Context, name string) (*Person, error) {
	id, err := newID()
	if err != nil {
		return nil, err
	}
	now := rfc3339(time.Now().UTC())
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO people (id, name, created_at, updated_at)
		VALUES (?, ?, ?, ?)`, id, name, now, now)
	if err != nil {
		return nil, fmt.Errorf("create person: %w", err)
	}
	return &Person{ID: id, Name: name, CreatedAt: time.Now().UTC()}, nil
}

// RenamePerson updates a person's display name.
func (s *FaceStore) RenamePerson(ctx context.Context, id, name string) error {
	now := rfc3339(time.Now().UTC())
	res, err := s.db.ExecContext(ctx,
		`UPDATE people SET name = ?, updated_at = ? WHERE id = ?`, name, now, id)
	if err != nil {
		return fmt.Errorf("rename person: %w", err)
	}
	return ensureAffected(res, "person")
}

// SetCover writes which face ("derived") backs a person's cover thumbnail.
func (s *FaceStore) SetCover(ctx context.Context, personID, faceID string) error {
	f, err := s.FaceByID(ctx, faceID)
	if err != nil {
		return err
	}
	if f == nil || f.ID == "" {
		return fmt.Errorf("face %q not found", faceID)
	}
	now := rfc3339(time.Now().UTC())
	res, err := s.db.ExecContext(ctx,
		`UPDATE people SET cover_face_id = ?, cover_file_id = ?, updated_at = ?
		 WHERE id = ?`, faceID, f.FileID, now, personID)
	if err != nil {
		return fmt.Errorf("set cover: %w", err)
	}
	return ensureAffected(res, "person")
}

// DeletePerson removes a person and their assignments (faces are kept).
func (s *FaceStore) DeletePerson(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM people WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete person: %w", err)
	}
	return ensureAffected(res, "person")
}

// Merge moves every face of source into person keep, preserving each
// assignment's provenance (manual wins when a face somehow appears twice),
// adopts source's cover when the target lacks one, and deletes source.
func (s *FaceStore) Merge(ctx context.Context, keepID, sourceID string) error {
	return s.MergeAs(ctx, keepID, sourceID, "")
}

// MergeAs is Merge, but when by is non-empty every moved face is recorded as
// assigned that way. A person merging two groups by hand says "these are the
// same", so those faces become "manual": trusted evidence that later grouping
// compares new faces against, and that automatic passes never undo.
func (s *FaceStore) MergeAs(ctx context.Context, keepID, sourceID, by string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("merge begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
		SELECT face_id, assigned_by FROM person_faces WHERE person_id = ?`, sourceID)
	if err != nil {
		return fmt.Errorf("merge list: %w", err)
	}
	type mv struct{ faceID, by string }
	var moves []mv
	for rows.Next() {
		var m mv
		if err := rows.Scan(&m.faceID, &m.by); err != nil {
			_ = rows.Close()
			return fmt.Errorf("merge scan: %w", err)
		}
		moves = append(moves, m)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("merge rows: %w", err)
	}

	for _, m := range moves {
		assigned := m.by
		if by != "" {
			assigned = by
		}
		var existing string
		err := tx.QueryRowContext(ctx,
			`SELECT assigned_by FROM person_faces WHERE person_id = ? AND face_id = ?`,
			keepID, m.faceID).Scan(&existing)
		switch {
		case err == sql.ErrNoRows:
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO person_faces (person_id, face_id, assigned_by, created_at)
				 VALUES (?, ?, ?, ?)`, keepID, m.faceID, assigned, rfc3339(time.Now().UTC())); err != nil {
				return fmt.Errorf("merge insert: %w", err)
			}
		case err != nil:
			return fmt.Errorf("merge probe: %w", err)
		case existing == "auto" && assigned == "manual":
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM person_faces WHERE person_id = ? AND face_id = ?`,
				keepID, m.faceID); err != nil {
				return fmt.Errorf("merge delete clash: %w", err)
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO person_faces (person_id, face_id, assigned_by, created_at)
				 VALUES (?, ?, ?, ?)`, keepID, m.faceID, assigned, rfc3339(time.Now().UTC())); err != nil {
				return fmt.Errorf("merge insert manual: %w", err)
			}
		}
	}

	// Adopt the source cover when the target has none.
	var hasCover string
	err = tx.QueryRowContext(ctx,
		`SELECT COALESCE(cover_face_id, '') FROM people WHERE id = ?`, keepID).Scan(&hasCover)
	if err != nil {
		return fmt.Errorf("merge cover probe: %w", err)
	}
	if hasCover == "" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE people SET cover_face_id = (SELECT cover_face_id FROM people WHERE id = ?),
			                  cover_file_id = (SELECT cover_file_id FROM people WHERE id = ?),
			                  updated_at = ?
			WHERE id = ?`, sourceID, sourceID, rfc3339(time.Now().UTC()), keepID); err != nil {
			return fmt.Errorf("merge cover: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM people WHERE id = ?`, sourceID); err != nil {
		return fmt.Errorf("merge delete source: %w", err)
	}
	return tx.Commit()
}

// AssignPerson assigns a face to a person. A face belongs to at most one
// person; assigning it elsewhere moves it first. assignedBy is 'auto' or
// 'manual' and is stored verbatim (a face moved off another person is always
// 'manual'). Covers of both people are kept pointing at one of their own
// faces, and an automatic "Person N" left with no faces is removed.
func (s *FaceStore) AssignPerson(ctx context.Context, personID, faceID, assignedBy string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("assign begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var previous []string
	rows, err := tx.QueryContext(ctx,
		`SELECT person_id FROM person_faces WHERE face_id = ? AND person_id <> ?`, faceID, personID)
	if err != nil {
		return fmt.Errorf("assign lookup: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return fmt.Errorf("assign lookup: %w", err)
		}
		previous = append(previous, id)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("assign lookup: %w", err)
	}
	if len(previous) > 0 {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM person_faces WHERE face_id = ? AND person_id <> ?`, faceID, personID); err != nil {
			return fmt.Errorf("assign unlink: %w", err)
		}
		// Moved a face from another person: that is a human decision, and it
		// must survive regrouping.
		assignedBy = "manual"
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO person_faces (person_id, face_id, assigned_by, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(person_id, face_id) DO UPDATE SET assigned_by = excluded.assigned_by`,
		personID, faceID, assignedBy, rfc3339(time.Now().UTC())); err != nil {
		return fmt.Errorf("assign insert: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM face_holds WHERE face_id = ?`, faceID); err != nil {
		return fmt.Errorf("release face: %w", err)
	}
	if err := tidyPeopleTx(ctx, tx, append(previous, personID)...); err != nil {
		return err
	}
	return tx.Commit()
}

// UnassignPerson removes a face from a person (it becomes cluster-eligible).
func (s *FaceStore) UnassignPerson(ctx context.Context, personID, faceID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("unassign begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx,
		`DELETE FROM person_faces WHERE person_id = ? AND face_id = ?`, personID, faceID)
	if err != nil {
		return fmt.Errorf("unassign face: %w", err)
	}
	if err := ensureAffected(res, "assignment"); err != nil {
		return err
	}
	// "Not this person" is a decision: keep automatic grouping from putting
	// the face straight back.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO face_holds (face_id, created_at) VALUES (?, ?)
		ON CONFLICT(face_id) DO NOTHING`, faceID, rfc3339(time.Now().UTC())); err != nil {
		return fmt.Errorf("hold face: %w", err)
	}
	if err := tidyPeopleTx(ctx, tx, personID); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteFace removes one face for good (a false detection, or someone the
// user does not want kept). Its photo stays recorded as scanned, so the face
// is not detected again; the person it belonged to keeps a valid cover.
func (s *FaceStore) DeleteFace(ctx context.Context, faceID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete face begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var owners []string
	rows, err := tx.QueryContext(ctx, `SELECT person_id FROM person_faces WHERE face_id = ?`, faceID)
	if err != nil {
		return fmt.Errorf("delete face lookup: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return fmt.Errorf("delete face lookup: %w", err)
		}
		owners = append(owners, id)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("delete face lookup: %w", err)
	}
	// Covers point at faces with ON DELETE SET NULL, so the cover refresh
	// below picks a replacement.
	res, err := tx.ExecContext(ctx, `DELETE FROM faces WHERE id = ?`, faceID)
	if err != nil {
		return fmt.Errorf("delete face: %w", err)
	}
	if err := ensureAffected(res, "face"); err != nil {
		return err
	}
	if err := tidyPeopleTx(ctx, tx, owners...); err != nil {
		return err
	}
	return tx.Commit()
}

// DeletePersonAndFaces removes a person together with every face assigned to
// them, so the group is not rebuilt by the next grouping pass. Returns the
// number of faces removed.
func (s *FaceStore) DeletePersonAndFaces(ctx context.Context, personID string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("delete person begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `
		DELETE FROM faces WHERE id IN (SELECT face_id FROM person_faces WHERE person_id = ?)`, personID)
	if err != nil {
		return 0, fmt.Errorf("delete person faces: %w", err)
	}
	n, _ := res.RowsAffected()
	res, err = tx.ExecContext(ctx, `DELETE FROM people WHERE id = ?`, personID)
	if err != nil {
		return 0, fmt.Errorf("delete person: %w", err)
	}
	if err := ensureAffected(res, "person"); err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}

// tidyPeopleTx keeps each person's cover on one of their own faces (the
// largest, most confident one when it has to change) and deletes automatic
// "Person N" entries that no longer have any face. Named people are kept even
// when empty.
func tidyPeopleTx(ctx context.Context, tx *sql.Tx, personIDs ...string) error {
	now := rfc3339(time.Now().UTC())
	for _, pid := range personIDs {
		if _, err := tx.ExecContext(ctx, `
			UPDATE people SET
			    cover_face_id = (SELECT fa.id FROM person_faces pf JOIN faces fa ON fa.id = pf.face_id
			                     WHERE pf.person_id = people.id
			                     ORDER BY fa.width * fa.height * fa.confidence DESC, fa.id LIMIT 1),
			    cover_file_id = (SELECT fa.file_id FROM person_faces pf JOIN faces fa ON fa.id = pf.face_id
			                     WHERE pf.person_id = people.id
			                     ORDER BY fa.width * fa.height * fa.confidence DESC, fa.id LIMIT 1),
			    updated_at = ?
			WHERE id = ? AND (cover_face_id IS NULL
			      OR cover_face_id NOT IN (SELECT face_id FROM person_faces WHERE person_id = ?))`,
			now, pid, pid); err != nil {
			return fmt.Errorf("refresh cover: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM people WHERE id = ? AND name GLOB 'Person [0-9]*'
			  AND NOT EXISTS (SELECT 1 FROM person_faces WHERE person_id = ?)`, pid, pid); err != nil {
			return fmt.Errorf("drop empty person: %w", err)
		}
	}
	return nil
}

// PurgeFaces deletes every face observation and assignment, leaving people
// (names, and any manual curation) intact. Covers are dropped via
// ON DELETE SET NULL. Returns the number of faces removed.
func (s *FaceStore) PurgeFaces(ctx context.Context) (int, error) {
	n, err := s.CountFaces(ctx)
	if err != nil {
		return 0, err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM faces`); err != nil {
		return 0, fmt.Errorf("purge faces: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM face_scans`); err != nil {
		return 0, fmt.Errorf("purge face scans: %w", err)
	}
	// Person faces cascade with the faces above; empty them defensively too.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM person_faces`); err != nil {
		return 0, fmt.Errorf("purge assignments: %w", err)
	}
	return n, nil
}

// PurgeStaleFaces removes faces recorded by any other provider or version:
// descriptors from different algorithms are not comparable, so they must not
// be mixed into clustering. People left with no faces and an automatic
// "Person N" name go too; names a user chose are kept.
func (s *FaceStore) PurgeStaleFaces(ctx context.Context, provider string, version int) (int, error) {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM face_scans WHERE NOT (provider = ? AND version = ?)`, provider, version); err != nil {
		return 0, fmt.Errorf("purge stale face scans: %w", err)
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM faces WHERE NOT (provider = ? AND version = ?)`, provider, version)
	if err != nil {
		return 0, fmt.Errorf("purge stale faces: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return 0, nil
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM person_faces WHERE face_id NOT IN (SELECT id FROM faces)`); err != nil {
		return 0, fmt.Errorf("purge stale assignments: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM people
		WHERE id NOT IN (SELECT person_id FROM person_faces)
		  AND name GLOB 'Person [0-9]*'`); err != nil {
		return 0, fmt.Errorf("purge empty people: %w", err)
	}
	return int(n), nil
}

// Exemplar is one stored face descriptor of a person, with whether a human
// vouched for it (assigned or merged by hand).
type Exemplar struct {
	Descriptor []float32
	Manual     bool
}

// PersonExemplars returns up to perPerson descriptors for every person, the
// hand-confirmed ones first and then the newest. Matching a new face against
// individual exemplars (not only the person's average) is what lets a person
// whose two looks were merged be recognised as either.
func (s *FaceStore) PersonExemplars(ctx context.Context, perPerson int) (map[string][]Exemplar, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT pf.person_id, fa.descriptor, pf.assigned_by
		FROM person_faces pf
		JOIN faces fa ON fa.id = pf.face_id
		ORDER BY pf.person_id, (pf.assigned_by = 'manual') DESC, pf.created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list exemplars: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string][]Exemplar{}
	for rows.Next() {
		var pid, by string
		var blob []byte
		if err := rows.Scan(&pid, &blob, &by); err != nil {
			return nil, fmt.Errorf("scan exemplar: %w", err)
		}
		if len(out[pid]) >= perPerson {
			continue
		}
		d, err := float32FromBytes(blob)
		if err != nil {
			return nil, err
		}
		out[pid] = append(out[pid], Exemplar{Descriptor: d, Manual: by == "manual"})
	}
	return out, rows.Err()
}

// AutoNamedPeople returns the people Cairn named itself ("Person 12") that no
// human has touched: no hand-assigned face. Only these may be merged
// automatically.
func (s *FaceStore) AutoNamedPeople(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.id FROM people p
		WHERE p.name GLOB 'Person [0-9]*'
		  AND NOT EXISTS (SELECT 1 FROM person_faces pf
		                  WHERE pf.person_id = p.id AND pf.assigned_by = 'manual')`)
	if err != nil {
		return nil, fmt.Errorf("list auto people: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// Float32 BLOB helpers -----------------------------------------------------

func float32Bytes(v []float32) ([]byte, error) {
	out := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(f))
	}
	return out, nil
}

func float32FromBytes(b []byte) ([]float32, error) {
	if len(b)%4 != 0 {
		return nil, fmt.Errorf("descriptor blob length %d not multiple of 4", len(b))
	}
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return out, nil
}

// scanDescriptor reads the descriptor column into f, expects it to be the
// final scanned column after the Time fields (callers use custom Scan).
// scanFaceFull scans the shared 13-column face+rel_path row layout used by
// FacesUnassigned and PersonFaces: id, file_id, provider, version, x, y,
// width, height, confidence, descriptor, created_at, updated_at, rel_path.
func scanFaceFull(rows *sql.Rows) (FaceRecord, error) {
	var (
		f                            FaceRecord
		descriptor, created, updated string
	)
	err := rows.Scan(&f.ID, &f.FileID, &f.Provider, &f.Version,
		&f.Box.X, &f.Box.Y, &f.Box.Width, &f.Box.Height, &f.Box.Confidence,
		&descriptor, &created, &updated, &f.RelPath)
	if err != nil {
		return FaceRecord{}, fmt.Errorf("scan face: %w", err)
	}
	desc, err := float32FromBytes([]byte(descriptor))
	if err != nil {
		return FaceRecord{}, err
	}
	f.Descriptor = desc
	if err := parseTime(created, &f.CreatedAt); err != nil {
		return FaceRecord{}, err
	}
	if err := parseTime(updated, &f.UpdatedAt); err != nil {
		return FaceRecord{}, err
	}
	return f, nil
}

// newID returns a random lowercase-hex id (32 digits) using the same scheme
// as other Cairn entities.
func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// ErrPersonNotFound is returned when a person does not exist.
var ErrPersonNotFound = errors.New("person not found")

func ensureAffected(res sql.Result, what string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if n == 0 {
		return ErrPersonNotFound
	}
	return nil
}

// groupingState loads what planGroups needs: every face with its current
// assignment (free when unassigned or held by an untouched automatic group),
// the exemplars of every anchored person, and the untouched automatic people.
func (s *FaceStore) groupingState(ctx context.Context) ([]clusterFace, map[string][]Exemplar, map[string]bool, error) {
	auto, err := s.AutoNamedPeople(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT fa.id, fa.width, fa.height, fa.confidence, fa.descriptor,
		       COALESCE(pf.person_id, ''),
		       EXISTS (SELECT 1 FROM face_holds h WHERE h.face_id = fa.id)
		FROM faces fa
		LEFT JOIN person_faces pf ON pf.face_id = fa.id
		ORDER BY fa.created_at, fa.id`)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("list faces for grouping: %w", err)
	}
	var faces []clusterFace
	seen := map[string]bool{}
	for rows.Next() {
		var (
			f    clusterFace
			w, h int
			conf float64
			raw  []byte
			held bool
		)
		if err := rows.Scan(&f.ID, &w, &h, &conf, &raw, &f.PersonID, &held); err != nil {
			_ = rows.Close()
			return nil, nil, nil, fmt.Errorf("scan face for grouping: %w", err)
		}
		if seen[f.ID] {
			continue
		}
		seen[f.ID] = true
		if f.Desc, err = float32FromBytes(raw); err != nil {
			_ = rows.Close()
			return nil, nil, nil, err
		}
		f.Quality = float64(w*h) * conf
		f.Free = (f.PersonID == "" && !held) || auto[f.PersonID]
		faces = append(faces, f)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, nil, nil, fmt.Errorf("list faces for grouping: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, nil, nil, err
	}
	all, err := s.PersonExemplars(ctx, maxExemplars)
	if err != nil {
		return nil, nil, nil, err
	}
	anchors := make(map[string][]Exemplar, len(all))
	for pid, ex := range all {
		if !auto[pid] {
			anchors[pid] = ex
		}
	}
	return faces, anchors, auto, nil
}

// applyGroupPlan writes a plan from planGroups in one transaction: moves free
// faces, creates "Person N" for new groups, refreshes covers that no longer
// show one of the person's faces, and deletes automatic people left empty.
func (s *FaceStore) applyGroupPlan(ctx context.Context, faces []clusterFace, plan *groupPlan) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("grouping begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := rfc3339(time.Now().UTC())

	// New people are numbered after the highest "Person N" in use.
	next := 1
	nameRows, err := tx.QueryContext(ctx, `SELECT name FROM people WHERE name GLOB 'Person [0-9]*'`)
	if err != nil {
		return fmt.Errorf("grouping names: %w", err)
	}
	for nameRows.Next() {
		var name string
		if err := nameRows.Scan(&name); err != nil {
			_ = nameRows.Close()
			return err
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(name, "Person ")); err == nil && n >= next {
			next = n + 1
		}
	}
	if err := nameRows.Close(); err != nil {
		return err
	}
	created := make(map[string]string, len(plan.NewGroups))
	for i := range plan.NewGroups {
		id, err := newID()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO people (id, name, created_at, updated_at) VALUES (?, ?, ?, ?)`,
			id, fmt.Sprintf("Person %d", next), now, now); err != nil {
			return fmt.Errorf("grouping create person: %w", err)
		}
		next++
		created[newGroupKey(i)] = id
	}
	resolve := func(key string) string {
		if id, ok := created[key]; ok {
			return id
		}
		return key
	}

	for _, f := range faces {
		if !f.Free {
			continue
		}
		to := resolve(plan.Assign[f.ID])
		if to == f.PersonID {
			continue
		}
		if f.PersonID != "" {
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM person_faces WHERE face_id = ? AND assigned_by = 'auto'`, f.ID); err != nil {
				return fmt.Errorf("grouping unlink: %w", err)
			}
		}
		if to != "" {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO person_faces (person_id, face_id, assigned_by, created_at)
				VALUES (?, ?, 'auto', ?)
				ON CONFLICT(person_id, face_id) DO NOTHING`, to, f.ID, now); err != nil {
				return fmt.Errorf("grouping assign: %w", err)
			}
		}
	}

	for key, faceID := range plan.Covers {
		pid := resolve(key)
		if _, err := tx.ExecContext(ctx, `
			UPDATE people
			SET cover_face_id = ?, cover_file_id = (SELECT file_id FROM faces WHERE id = ?), updated_at = ?
			WHERE id = ? AND (cover_face_id IS NULL
			      OR cover_face_id NOT IN (SELECT face_id FROM person_faces WHERE person_id = ?))`,
			faceID, faceID, now, pid, pid); err != nil {
			return fmt.Errorf("grouping cover: %w", err)
		}
	}
	for _, pid := range plan.Dropped {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM people WHERE id = ?
			  AND NOT EXISTS (SELECT 1 FROM person_faces WHERE person_id = ?)`, pid, pid); err != nil {
			return fmt.Errorf("grouping drop person: %w", err)
		}
	}
	return tx.Commit()
}
