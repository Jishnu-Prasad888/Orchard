package httpapi

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/auth"
	"github.com/Jishnu-Prasad888/Orchard/internal/authz"
	"github.com/Jishnu-Prasad888/Orchard/internal/library"
	"github.com/Jishnu-Prasad888/Orchard/internal/media"
	"github.com/Jishnu-Prasad888/Orchard/internal/uploads"
)

// Resumable uploads (see internal/uploads for the on-disk side):
//
//	POST   /libraries/{id}/uploads                    {path, size} → session
//	GET    /libraries/{id}/uploads/{uploadID}         how much has arrived
//	PATCH  /libraries/{id}/uploads/{uploadID}         one chunk; Upload-Offset header
//	POST   /libraries/{id}/uploads/{uploadID}/complete → the library file
//	DELETE /libraries/{id}/uploads/{uploadID}         cancel
//
// Every failure a client can recover from by retrying has its own code, so
// the browser can keep going on its own and only tell the person about the
// failures they have to act on.

// uploadStallTimeout is how long a chunk may go without a single byte
// arriving before the server gives up on the connection. It is a stall
// timer, not a deadline, so a slow but steady link is never cut off. A
// variable only so tests can shorten it.
var uploadStallTimeout = 30 * time.Second

const (
	// maxConcurrentChunkWrites bounds simultaneous chunk writes server-wide.
	// A Raspberry Pi's SD card or USB disk slows to a crawl when many writers
	// fsync at once; past this, clients are asked to retry shortly.
	maxConcurrentChunkWrites = 4
)

// Error codes specific to uploads.
const (
	CodeInsufficientStorage  = "INSUFFICIENT_STORAGE"
	CodeStorageUnwritable    = "STORAGE_UNWRITABLE"
	CodeStorageError         = "STORAGE_ERROR"
	CodeUploadNotFound       = "UPLOAD_NOT_FOUND"
	CodeUploadOffsetMismatch = "UPLOAD_OFFSET_MISMATCH"
	CodeUploadIncomplete     = "UPLOAD_INCOMPLETE"
	CodeUploadBusy           = "UPLOAD_BUSY"
	CodeUploadStalled        = "UPLOAD_STALLED"
	CodeUploadInterrupted    = "UPLOAD_INTERRUPTED"
)

type uploadResponse struct {
	ID           string        `json:"id"`
	Path         string        `json:"path"`
	Size         int64         `json:"size"`
	Offset       int64         `json:"offset"`
	ChunkSize    int64         `json:"chunk_size"`
	MaxChunkSize int64         `json:"max_chunk_size"`
	Complete     bool          `json:"complete"`
	File         *fileResponse `json:"file,omitempty"`
}

func toUploadResponse(sess *uploads.Session) uploadResponse {
	return uploadResponse{
		ID: sess.ID, Path: sess.Path, Size: sess.Size, Offset: sess.Offset,
		ChunkSize: uploads.DefaultChunkSize, MaxChunkSize: uploads.MaxChunkSize,
		Complete: sess.Committed(),
	}
}

// uploadLock returns the mutex serialising work on one upload, so a retry
// cannot interleave with an earlier attempt the server is still reading.
func (s *Server) uploadLock(id string) *sync.Mutex {
	mu, _ := s.uploadLocks.LoadOrStore(id, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// handleCreateUpload — POST /api/v1/libraries/{id}/uploads
func (s *Server) handleCreateUpload(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var body struct {
		Path string `json:"path"`
		Size *int64 `json:"size"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	lib, ok := s.onlineLibrary(w, r, u)
	if !ok {
		return
	}
	reqID := requestIDOrEmpty(r)
	safe, err := media.SafeRelPath(body.Path)
	if err != nil || safe == "" {
		writeError(w, s.logger, reqID, http.StatusBadRequest, CodeBadRequest,
			"That file name or folder isn’t allowed.")
		return
	}
	if !media.IsBook(safe) {
		writeErrorf(w, s.logger, reqID, http.StatusUnsupportedMediaType, CodeUnsupportedMediaType,
			"“%s” isn’t an EPUB or PDF. Only EPUB and PDF files can be added to a library.", path.Base(safe))
		return
	}
	if body.Size == nil || *body.Size < 0 {
		writeError(w, s.logger, reqID, http.StatusBadRequest, CodeBadRequest,
			"The upload’s size is missing.")
		return
	}
	size := *body.Size
	if limit := s.uploadLimit(); size > limit {
		writeErrorf(w, s.logger, reqID, http.StatusRequestEntityTooLarge, CodePayloadTooLarge,
			"“%s” is %s, larger than this server’s %s upload limit.",
			path.Base(safe), humanBytes(size), humanBytes(limit))
		return
	}
	if !s.requireCap(w, r, u, parentFolderKey(lib.ID, safe), authz.CapCreate) {
		return
	}
	if _, err := os.Stat(filepath.Join(lib.Root, filepath.FromSlash(safe))); err == nil {
		writeError(w, s.logger, reqID, http.StatusConflict, CodeConflict, alreadyExistsMessage(safe))
		return
	}
	if free, known := uploads.FreeBytes(lib.Root); known && free < size+uploads.ReserveBytes {
		available := max(free-uploads.ReserveBytes, 0)
		writeErrorf(w, s.logger, reqID, http.StatusInsufficientStorage, CodeInsufficientStorage,
			"Not enough space on the server: “%s” needs %s, but only %s is free.",
			path.Base(safe), humanBytes(size), humanBytes(available))
		return
	}

	store := uploads.Open(lib.Root)
	store.Sweep(time.Now())
	sess, err := store.Create(lib.ID, u.ID, safe, size)
	if err != nil {
		s.writeUploadError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, map[string]any{"upload": toUploadResponse(sess)})
}

// handleGetUpload — GET /api/v1/libraries/{id}/uploads/{uploadID}
func (s *Server) handleGetUpload(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, _, sess, ok := s.loadUpload(w, r, u)
	if !ok {
		return
	}
	resp := toUploadResponse(sess)
	if sess.Committed() {
		resp.File = s.committedFile(w, r, lib, sess)
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"upload": resp})
}

// handlePatchUpload — PATCH /api/v1/libraries/{id}/uploads/{uploadID}
//
// The body is the raw bytes of one chunk, starting at the Upload-Offset
// header. It streams straight to disk through a small pooled buffer, so
// memory use stays flat however big the file is.
func (s *Server) handlePatchUpload(w http.ResponseWriter, r *http.Request, u *auth.User) {
	reqID := requestIDOrEmpty(r)
	offset, err := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)
	if err != nil || offset < 0 {
		writeError(w, s.logger, reqID, http.StatusBadRequest, CodeBadRequest,
			"The chunk’s Upload-Offset header is missing or invalid.")
		return
	}
	if r.ContentLength > uploads.MaxChunkSize {
		writeErrorf(w, s.logger, reqID, http.StatusRequestEntityTooLarge, CodePayloadTooLarge,
			"A single chunk can be at most %s.", humanBytes(uploads.MaxChunkSize))
		return
	}

	select {
	case s.uploadSlots <- struct{}{}:
		defer func() { <-s.uploadSlots }()
	default:
		w.Header().Set("Retry-After", "2")
		writeError(w, s.logger, reqID, http.StatusServiceUnavailable, CodeUploadBusy,
			"The server is busy with other uploads; this one will continue in a moment.")
		return
	}

	id := r.PathValue("uploadID")
	mu := s.uploadLock(id)
	if !mu.TryLock() {
		w.Header().Set("Retry-After", "2")
		writeError(w, s.logger, reqID, http.StatusConflict, CodeUploadBusy,
			"The server is still receiving an earlier attempt at this chunk.")
		return
	}
	defer mu.Unlock()

	_, store, sess, ok := s.loadUpload(w, r, u)
	if !ok {
		return
	}

	rc := http.NewResponseController(w)
	body := newStallReader(http.MaxBytesReader(w, r.Body, uploads.MaxChunkSize), rc)
	_, err = store.Append(sess, offset, body)
	body.done()
	if err != nil {
		s.writeUploadError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"upload": toUploadResponse(sess)})
}

// handleCompleteUpload — POST /api/v1/libraries/{id}/uploads/{uploadID}/complete
//
// Idempotent: asking again after success returns the same file, so a client
// whose first answer was lost on the way back is not told it failed.
func (s *Server) handleCompleteUpload(w http.ResponseWriter, r *http.Request, u *auth.User) {
	reqID := requestIDOrEmpty(r)
	id := r.PathValue("uploadID")
	mu := s.uploadLock(id)
	if !mu.TryLock() {
		w.Header().Set("Retry-After", "2")
		writeError(w, s.logger, reqID, http.StatusConflict, CodeUploadBusy,
			"The server is still receiving this upload’s last chunk.")
		return
	}
	defer mu.Unlock()

	lib, store, sess, ok := s.loadUpload(w, r, u)
	if !ok {
		return
	}
	if sess.Committed() {
		if file := s.committedFile(w, r, lib, sess); file != nil {
			writeJSON(w, s.logger, http.StatusOK, map[string]any{"file": file})
			return
		}
		writeError(w, s.logger, reqID, http.StatusNotFound, CodeNotFound,
			"The uploaded file is no longer in the library.")
		return
	}
	if sess.Offset < sess.Size {
		writeErrorDetails(w, s.logger, reqID, http.StatusConflict, CodeUploadIncomplete,
			fmt.Sprintf("Only %s of %s has arrived.", humanBytes(sess.Offset), humanBytes(sess.Size)),
			map[string]any{"offset": sess.Offset})
		return
	}
	// Permissions may have changed since the upload began.
	if !s.requireCap(w, r, u, parentFolderKey(lib.ID, sess.Path), authz.CapCreate) {
		return
	}
	svc, cleanup, ok := s.openMediaService(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	created, err := svc.CommitUpload(r.Context(), sess.Path, store.DataPath(sess.ID))
	if err != nil {
		s.writeUploadError(w, r, err, sess.Path)
		return
	}
	if err := store.MarkCommitted(sess, created.ID); err != nil {
		// The file is in the library; only the retry-safety record failed.
		s.logger.Warn("record committed upload", "upload_id", sess.ID, "error", err)
	}
	s.uploadLocks.Delete(id)
	writeJSON(w, s.logger, http.StatusCreated, map[string]any{"file": toFileResponse(created)})
}

// handleDeleteUpload — DELETE /api/v1/libraries/{id}/uploads/{uploadID}
func (s *Server) handleDeleteUpload(w http.ResponseWriter, r *http.Request, u *auth.User) {
	_, store, sess, ok := s.loadUpload(w, r, u)
	if !ok {
		return
	}
	if err := store.Remove(sess.ID); err != nil {
		s.writeUploadError(w, r, err)
		return
	}
	s.uploadLocks.Delete(sess.ID)
	w.WriteHeader(http.StatusNoContent)
}

// onlineLibrary resolves the {id} library and refuses an offline one.
func (s *Server) onlineLibrary(w http.ResponseWriter, r *http.Request, u *auth.User) (*library.Library, bool) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return nil, false
	}
	if lib.Status == library.StatusOffline {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusConflict, CodeConflict,
			"This library’s disk is offline, so nothing can be uploaded to it right now.")
		return nil, false
	}
	return lib, true
}

// loadUpload resolves the library and the caller's own session. Someone
// else's session is reported as missing rather than forbidden.
func (s *Server) loadUpload(w http.ResponseWriter, r *http.Request, u *auth.User) (
	*library.Library, *uploads.Store, *uploads.Session, bool,
) {
	lib, ok := s.onlineLibrary(w, r, u)
	if !ok {
		return nil, nil, nil, false
	}
	store := uploads.Open(lib.Root)
	sess, err := store.Get(r.PathValue("uploadID"))
	if err == nil && (sess.UserID != u.ID || sess.LibraryID != lib.ID) {
		err = uploads.ErrNotFound
	}
	if err != nil {
		s.writeUploadError(w, r, err)
		return nil, nil, nil, false
	}
	return lib, store, sess, true
}

// committedFile looks up the library file a finished session became.
func (s *Server) committedFile(w http.ResponseWriter, r *http.Request, lib *library.Library, sess *uploads.Session) *fileResponse {
	svc, cleanup, ok := s.openMediaService(w, r, lib)
	if !ok {
		return nil
	}
	defer cleanup()
	f, err := svc.Store().GetByID(r.Context(), sess.FileID)
	if err != nil {
		return nil
	}
	resp := toFileResponse(f)
	return &resp
}

func (s *Server) uploadLimit() int64 {
	if s.maxUploadBytes > 0 {
		return s.maxUploadBytes
	}
	return media.DefaultMaxUploadBytes
}

// writeUploadError turns anything that can go wrong while receiving or
// saving an upload into a message a person can act on. dest, when given,
// names the file in a "file already exists" conflict.
func (s *Server) writeUploadError(w http.ResponseWriter, r *http.Request, err error, dest ...string) {
	reqID := requestIDOrEmpty(r)
	var mismatch *uploads.OffsetMismatchError
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &mismatch):
		writeErrorDetails(w, s.logger, reqID, http.StatusConflict, CodeUploadOffsetMismatch,
			"The server already has part of this chunk; resuming from where it left off.",
			map[string]any{"offset": mismatch.Current})
	case errors.Is(err, uploads.ErrNotFound):
		writeError(w, s.logger, reqID, http.StatusNotFound, CodeUploadNotFound,
			"This upload expired or was cancelled on the server.")
	case errors.Is(err, uploads.ErrCommitted):
		writeError(w, s.logger, reqID, http.StatusConflict, CodeConflict,
			"This upload has already finished.")
	case errors.Is(err, uploads.ErrTooMuchData):
		writeError(w, s.logger, reqID, http.StatusBadRequest, CodeBadRequest,
			"More data arrived than the file’s declared size.")
	case errors.As(err, &tooBig):
		writeErrorf(w, s.logger, reqID, http.StatusRequestEntityTooLarge, CodePayloadTooLarge,
			"The upload is larger than this server’s %s limit.", humanBytes(s.uploadLimit()))
	case errors.Is(err, media.ErrUploadTooLarge):
		writeErrorf(w, s.logger, reqID, http.StatusRequestEntityTooLarge, CodePayloadTooLarge,
			"The file is larger than this server’s %s upload limit.", humanBytes(s.uploadLimit()))
	case errors.Is(err, media.ErrNotABook):
		writeError(w, s.logger, reqID, http.StatusUnsupportedMediaType, CodeUnsupportedMediaType,
			"Only EPUB and PDF files can be added to a library.")
	case errors.Is(err, media.ErrAlreadyExists) && len(dest) > 0:
		writeError(w, s.logger, reqID, http.StatusConflict, CodeConflict, alreadyExistsMessage(dest[0]))
	case isStall(err):
		writeErrorf(w, s.logger, reqID, http.StatusRequestTimeout, CodeUploadStalled,
			"No data arrived for %d seconds, so the server stopped waiting.",
			int(uploadStallTimeout/time.Second))
	case isDisconnect(err):
		writeError(w, s.logger, reqID, http.StatusBadRequest, CodeUploadInterrupted,
			"The connection dropped partway through sending.")
	default:
		if status, code, msg, ok := storageFailure(err); ok {
			s.logger.Error("upload storage", "error", err)
			writeError(w, s.logger, reqID, status, code, msg)
			return
		}
		s.writeMediaError(w, r, err)
	}
}

// storageFailure recognises the disk problems a server owner can fix —
// a full disk, wrong permissions, a read-only mount — so the message says
// which one rather than "Internal server error".
func storageFailure(err error) (status int, code, msg string, ok bool) {
	switch {
	case errors.Is(err, syscall.ENOSPC), errors.Is(err, syscall.EDQUOT):
		return http.StatusInsufficientStorage, CodeInsufficientStorage,
			"The server’s disk is full. Free some space, then retry — the upload resumes where it stopped.", true
	case errors.Is(err, syscall.EROFS):
		return http.StatusInternalServerError, CodeStorageUnwritable,
			"This library’s disk is mounted read-only, so the server can’t save files to it.", true
	case errors.Is(err, os.ErrPermission):
		return http.StatusInternalServerError, CodeStorageUnwritable,
			"The server doesn’t have permission to write to this library’s folder.", true
	case errors.Is(err, syscall.EIO):
		return http.StatusInternalServerError, CodeStorageError,
			"The library’s disk reported a read/write error. Check the drive and its cable.", true
	}
	return 0, "", "", false
}

func isStall(err error) bool {
	var ne net.Error
	return errors.Is(err, os.ErrDeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout())
}

func isDisconnect(err error) bool {
	return errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE) || errors.Is(err, net.ErrClosed)
}

func alreadyExistsMessage(rel string) string {
	folder := path.Dir(rel)
	if folder == "." || folder == "" {
		return fmt.Sprintf("“%s” already exists in the library’s top folder.", path.Base(rel))
	}
	return fmt.Sprintf("“%s” already exists in “%s”.", path.Base(rel), folder)
}

// humanBytes formats a size the way the web UI's formatBytes does.
func humanBytes(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	value := float64(n)
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if value < 10 && unit > 0 {
		return fmt.Sprintf("%.1f %s", value, units[unit])
	}
	return fmt.Sprintf("%.0f %s", value, units[unit])
}

// stallReader extends the connection's read deadline before every read, so
// an upload is cut off only when data stops arriving — never merely for
// being slow. The server-wide ReadTimeout would otherwise end any request
// body that takes longer than a minute.
type stallReader struct {
	r  io.Reader
	rc *http.ResponseController
	// armed is false when the writer cannot set deadlines (tests).
	armed bool
}

func newStallReader(r io.Reader, rc *http.ResponseController) *stallReader {
	armed := rc.SetReadDeadline(time.Now().Add(uploadStallTimeout)) == nil
	if armed {
		// The write deadline started counting when the request arrived;
		// lift it while the body streams in, and restore one in done.
		_ = rc.SetWriteDeadline(time.Time{})
	}
	return &stallReader{r: r, rc: rc, armed: armed}
}

func (s *stallReader) Read(p []byte) (int, error) {
	if s.armed {
		_ = s.rc.SetReadDeadline(time.Now().Add(uploadStallTimeout))
	}
	return s.r.Read(p)
}

// done gives the response a fresh, bounded window to be written.
func (s *stallReader) done() {
	if s.armed {
		_ = s.rc.SetWriteDeadline(time.Now().Add(uploadStallTimeout))
	}
}
