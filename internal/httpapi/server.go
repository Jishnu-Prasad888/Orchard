package httpapi

import (
	"database/sql"
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
	"github.com/Jishnu-Prasad888/Cairn/internal/authz"
	"github.com/Jishnu-Prasad888/Cairn/internal/backups"
	"github.com/Jishnu-Prasad888/Cairn/internal/crypto"
	"github.com/Jishnu-Prasad888/Cairn/internal/indexer"
	"github.com/Jishnu-Prasad888/Cairn/internal/library"
	"github.com/Jishnu-Prasad888/Cairn/internal/metrics"
	"github.com/Jishnu-Prasad888/Cairn/internal/ml"
)

// Server is the Cairn HTTP application. It is a small composition of the
// routes and shared dependencies; individual handlers stay in their own
// files.
type Server struct {
	logger         *slog.Logger
	db             *sql.DB
	web            http.Handler
	auth           *auth.Service
	authz          *authz.Service
	libraries      *library.Manager
	indexer        *indexer.IndexManager
	ml             *ml.Manager
	faces          *ml.FaceManager
	mlRuntime      *ml.Runtime
	faceModel      *ml.ModelDownloader
	faceDetector   *ml.ModelDownloader
	backups        *backups.Manager
	keys           *crypto.Keys
	secureCookies  bool
	ratelimit      *rateLimiter
	maxUploadBytes int64
	metrics        *metrics.Registry
	// ready gates the /ready endpoint: true once startup completes, flipped
	// false by MarkNotReady before graceful shutdown so proxies drain.
	ready atomic.Bool
}

// Dependencies are the services the HTTP layer needs. Keeping them explicit
// here makes the API testable in isolation and prevents hidden globals.
type Dependencies struct {
	Logger *slog.Logger
	DB     *sql.DB
	// Auth resolves principals from sessions. It may be nil, in which case the
	// protected authentication and user-management endpoints return
	// UNAUTHORIZED.
	Auth *auth.Service
	// Authz evaluates resource-based permissions and public shares
	// (ADR-0005). It may be nil, in which case every library content route
	// remains admin-only (the pre-Phase-9 gate).
	Authz *authz.Service
	// Libraries registers and reconciles storage libraries. It may be nil, in
	// which case the library-management endpoints return INTERNAL.
	Libraries *library.Manager
	// Indexer manages per-library scan jobs and index status. It may be nil,
	// in which case the indexing endpoints return SERVICE_UNAVAILABLE.
	Indexer *indexer.IndexManager
	// ML runs optional local similarity passes. It may be nil, in which case
	// the ML endpoints return SERVICE_UNAVAILABLE.
	ML *ml.Manager
	// Faces runs optional local face recognition passes and people curation.
	// It may be nil, in which case the face endpoints return
	// SERVICE_UNAVAILABLE.
	Faces *ml.FaceManager
	// MLRuntime is the administrator's ML on/off switch. May be nil.
	MLRuntime *ml.Runtime
	// FaceModel downloads the face-recognition (ArcFace) model on request. May be nil.
	FaceModel *ml.ModelDownloader
	// FaceDetector downloads the SCRFD face-detector model on request. May be nil.
	FaceDetector *ml.ModelDownloader
	// Backups runs server and library backups. It may be nil, in which case
	// the backup-management endpoints return SERVICE_UNAVAILABLE.
	Backups *backups.Manager
	// Keys is the optional at-rest encryption key (Phase 13) used to seal the
	// .cairn identity and thumbnails. It may be nil or disabled, in which
	// case metadata is written in the clear.
	Keys *crypto.Keys
	// SecureCookies forces the Secure flag on session cookies even when the
	// server did not observe TLS (e.g. behind a TLS-terminating proxy).
	SecureCookies bool
	// WebUI serves the embedded (or overridden) frontend at "/". It may be
	// nil, in which case API routes still work but the web UI is unavailable.
	WebUI http.Handler
	// MaxUploadBytes caps the total size of single upload bodies; values <= 0
	// fall back to media.DefaultMaxUploadBytes.
	MaxUploadBytes int64
	// Metrics records per-request counters for /api/v1/metrics. It may be
	// nil, in which case the metrics endpoint reports NOT_FOUND and no
	// request statistics are collected.
	Metrics *metrics.Registry
}

// New returns a Server built from the given dependencies. The caller must
// call Handler() to obtain the http.Handler.
func New(deps Dependencies) *Server {
	s := &Server{
		logger:         deps.Logger,
		db:             deps.DB,
		web:            deps.WebUI,
		auth:           deps.Auth,
		authz:          deps.Authz,
		libraries:      deps.Libraries,
		indexer:        deps.Indexer,
		ml:             deps.ML,
		faces:          deps.Faces,
		mlRuntime:      deps.MLRuntime,
		faceModel:      deps.FaceModel,
		faceDetector:   deps.FaceDetector,
		backups:        deps.Backups,
		keys:           deps.Keys,
		secureCookies:  deps.SecureCookies,
		ratelimit:      newRateLimiter(),
		maxUploadBytes: deps.MaxUploadBytes,
		metrics:        deps.Metrics,
	}
	s.ready.Store(true)
	return s
}

// MarkNotReady flips the /ready endpoint to 503. Call it before the graceful
// shutdown begins so load balancers and orchestrators stop routing traffic to
// this instance while its remaining requests drain.
func (s *Server) MarkNotReady() {
	s.ready.Store(false)
}

// Handler builds the fully-middleware-wrapped http.Handler for this Server.
//
// Route policy: everything under /api/v1/ is registered explicitly; health,
// version, auth bootstrap/status/login are public, and the remaining auth and
// user-management routes require a session (admin role where noted). Unknown
// /api/ routes still receive the JSON 404 envelope.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/ready", s.handleReady)
	mux.HandleFunc("GET /api/v1/version", s.handleVersion)
	mux.HandleFunc("GET /api/v1/metrics", s.handleMetrics)

	// Public authentication surface.
	mux.HandleFunc("GET /api/v1/auth/status", s.handleAuthStatus)
	mux.HandleFunc("POST /api/v1/auth/bootstrap", s.handleBootstrap)
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)

	// Session-required surface.
	mux.HandleFunc("POST /api/v1/auth/logout", s.requireSession(s.handleLogout))
	mux.Handle("GET /api/v1/auth/me", s.withAuth(allowAny, s.handleMe))

	mux.Handle("GET /api/v1/system/capabilities", s.withAuth(allowAny, s.handleCapabilities))

	// Per-user preferences.
	mux.Handle("GET /api/v1/settings/memories", s.withAuth(allowAny, s.handleGetMemorySettings))
	mux.Handle("PATCH /api/v1/settings/memories", s.withAuth(allowAny, s.handleUpdateMemorySettings))

	// Admin surface.
	mux.Handle("GET /api/v1/users", s.withAuth(allowAdmin, s.handleListUsers))
	mux.Handle("POST /api/v1/users", s.withAuth(allowAdmin, s.handleCreateUser))
	mux.Handle("POST /api/v1/users/{id}/sessions/revoke", s.withAuth(allowAdmin, s.handleRevokeUserSessions))

	// Library surface. Listing is open to any session and filtered by the
	// caller's read capability, so a member can find the libraries it was
	// granted; registering, probing, refreshing, and unregistering are
	// server-level administration and stay admin-only. Resource-based
	// authorization governs access to content within a library.
	if s.libraries != nil {
		mux.Handle("GET /api/v1/libraries", s.withAuth(allowAny, s.handleListLibraries))
		mux.Handle("POST /api/v1/libraries", s.withAuth(allowAdmin, s.handleCreateLibrary))
		mux.Handle("POST /api/v1/libraries/probe", s.withAuth(allowAdmin, s.handleProbeLibrary))
		mux.Handle("GET /api/v1/fs/dirs", s.withAuth(allowAdmin, s.handleBrowseDirs))
		mux.Handle("GET /api/v1/libraries/{id}", s.withAuth(allowAny, s.handleGetLibrary))
		mux.Handle("POST /api/v1/libraries/{id}/refresh", s.withAuth(allowAdmin, s.handleRefreshLibrary))
		mux.Handle("DELETE /api/v1/libraries/{id}", s.withAuth(allowAdmin, s.handleDeleteLibrary))
		// Indexing surface (admin): scanning and index status are server
		// operations, not per-resource capabilities.
		mux.Handle("POST /api/v1/libraries/{id}/index", s.withAuth(allowAdmin, s.handleTriggerIndex))
		mux.Handle("GET /api/v1/libraries/{id}/index/status", s.withAuth(allowAdmin, s.handleIndexStatus))

		// Local ML surface (admin): similarity passing, status, and purge are
		// server operations like indexing. Reading "similar files" follows
		// per-resource capabilities like any other content route.
		if s.mlRuntime != nil {
			mux.Handle("GET /api/v1/ml/settings", s.withAuth(allowAny, s.handleGetMLSettings))
			mux.Handle("PUT /api/v1/ml/settings", s.withAuth(allowAdmin, s.handlePutMLSettings))
			if s.faceModel != nil {
				mux.Handle("GET /api/v1/ml/model", s.withAuth(allowAdmin, s.handleGetFaceModel))
				mux.Handle("POST /api/v1/ml/model/download", s.withAuth(allowAdmin, s.handleDownloadFaceModel))
			}
		}
		if s.ml != nil {
			mux.Handle("GET /api/v1/libraries/{id}/ml", s.withAuth(allowAdmin, s.handleMLStatus))
			mux.Handle("POST /api/v1/libraries/{id}/ml/similarity/pass", s.withAuth(allowAdmin, s.handleMLPass))
			mux.Handle("POST /api/v1/libraries/{id}/ml/purge", s.withAuth(allowAdmin, s.handleMLPurge))
			mux.Handle("GET /api/v1/libraries/{id}/files/{fileID}/similar", s.withAuth(allowAny, s.handleSimilarFiles))
			mux.Handle("GET /api/v1/libraries/{id}/ml/similarity/groups", s.withAuth(allowAny, s.handleSimilarityGroups))
		}
		if s.faces != nil {
			mux.Handle("GET /api/v1/libraries/{id}/ml/faces", s.withAuth(allowAdmin, s.handleFaceStatus))
			mux.Handle("POST /api/v1/libraries/{id}/ml/faces/pass", s.withAuth(allowAdmin, s.handleFacePass))
			mux.Handle("POST /api/v1/libraries/{id}/ml/faces/cluster", s.withAuth(allowAdmin, s.handleFaceCluster))
			mux.Handle("POST /api/v1/libraries/{id}/ml/faces/purge", s.withAuth(allowAdmin, s.handleFacePurge))

			// People curation reads follow resource capabilities like other
			// content routes; curation writes use the library edit/create/
			// delete capabilities.
			mux.Handle("GET /api/v1/libraries/{id}/people", s.withAuth(allowAny, s.handleListPeople))
			mux.Handle("POST /api/v1/libraries/{id}/people", s.withAuth(allowAny, s.handleCreatePerson))
			mux.Handle("GET /api/v1/libraries/{id}/people/{personID}", s.withAuth(allowAny, s.handleGetPerson))
			mux.Handle("POST /api/v1/libraries/{id}/people/{personID}/rename", s.withAuth(allowAny, s.handleRenamePerson))
			mux.Handle("POST /api/v1/libraries/{id}/people/{personID}/cover", s.withAuth(allowAny, s.handleSetPersonCover))
			mux.Handle("POST /api/v1/libraries/{id}/people/{personID}/merge", s.withAuth(allowAny, s.handleMergePeople))
			mux.Handle("DELETE /api/v1/libraries/{id}/people/{personID}", s.withAuth(allowAny, s.handleDeletePerson))
			mux.Handle("POST /api/v1/libraries/{id}/people/{personID}/faces/{faceID}", s.withAuth(allowAny, s.handleAssignFace))
			mux.Handle("DELETE /api/v1/libraries/{id}/people/{personID}/faces/{faceID}", s.withAuth(allowAny, s.handleUnassignFace))
			mux.Handle("GET /api/v1/libraries/{id}/faces", s.withAuth(allowAny, s.handleListFaces))
			mux.Handle("GET /api/v1/libraries/{id}/faces/{faceID}/image", s.withAuth(allowAny, s.handleFaceImage))
			mux.Handle("DELETE /api/v1/libraries/{id}/faces/{faceID}", s.withAuth(allowAny, s.handleDeleteFace))
		}

		// Permissions and shares administration (library scope).
		mux.Handle("GET /api/v1/libraries/{id}/permissions", s.withAuth(allowAny, s.handleListGrants))
		mux.Handle("POST /api/v1/libraries/{id}/permissions", s.withAuth(allowAny, s.handleCreateGrant))
		mux.Handle("DELETE /api/v1/libraries/{id}/permissions/{grantID}", s.withAuth(allowAny, s.handleRevokeGrant))
		mux.Handle("GET /api/v1/libraries/{id}/shares", s.withAuth(allowAny, s.handleListShares))
		mux.Handle("POST /api/v1/libraries/{id}/shares", s.withAuth(allowAny, s.handleCreateShare))
		mux.Handle("DELETE /api/v1/libraries/{id}/shares/{shareID}", s.withAuth(allowAny, s.handleRevokeShare))

		// Media/files surface. Capabilities are enforced per resource inside
		// each handler via requireCap.
		mux.Handle("GET /api/v1/libraries/{id}/files", s.withAuth(allowAny, s.handleListFiles))
		mux.Handle("GET /api/v1/libraries/{id}/files/counts", s.withAuth(allowAny, s.handleFileCounts))
		mux.Handle("GET /api/v1/libraries/{id}/files/duplicates", s.withAuth(allowAny, s.handleListDuplicates))
		mux.Handle("GET /api/v1/libraries/{id}/files/{fileID}", s.withAuth(allowAny, s.handleGetFile))
		mux.Handle("GET /api/v1/libraries/{id}/files/{fileID}/download", s.withAuth(allowAny, s.handleDownloadFile))
		mux.Handle("POST /api/v1/libraries/{id}/files/upload", s.withAuth(allowAny, s.handleUploadFile))
		mux.Handle("POST /api/v1/libraries/{id}/files/{fileID}/rename", s.withAuth(allowAny, s.handleRenameFile))
		mux.Handle("POST /api/v1/libraries/{id}/files/{fileID}/move", s.withAuth(allowAny, s.handleMoveFile))
		mux.Handle("POST /api/v1/libraries/{id}/files/{fileID}/copy", s.withAuth(allowAny, s.handleCopyFile))
		mux.Handle("DELETE /api/v1/libraries/{id}/files/{fileID}", s.withAuth(allowAny, s.handleDeleteFile))
		mux.Handle("POST /api/v1/libraries/{id}/files/{fileID}/restore", s.withAuth(allowAny, s.handleRestoreFile))
		mux.Handle("DELETE /api/v1/libraries/{id}/files/{fileID}/permanent", s.withAuth(allowAny, s.handlePermanentDeleteFile))
		mux.Handle("GET /api/v1/libraries/{id}/files/{fileID}/metadata", s.withAuth(allowAny, s.handleGetFileMetadata))
		mux.Handle("GET /api/v1/libraries/{id}/files/{fileID}/thumbnail", s.withAuth(allowAny, s.handleGetThumbnail))
		mux.Handle("PUT /api/v1/libraries/{id}/files/{fileID}/thumbnail", s.withAuth(allowAny, s.handlePutThumbnail))

		// Per-file Markdown notes (captions).
		mux.Handle("GET /api/v1/libraries/{id}/files/{fileID}/note", s.withAuth(allowAny, s.handleGetFileNote))
		mux.Handle("PUT /api/v1/libraries/{id}/files/{fileID}/note", s.withAuth(allowAny, s.handleSetFileNote))
		mux.Handle("DELETE /api/v1/libraries/{id}/files/{fileID}/note", s.withAuth(allowAny, s.handleClearFileNote))

		// Folders and trash.
		mux.Handle("GET /api/v1/libraries/{id}/folders", s.withAuth(allowAny, s.handleListFolders))
		mux.Handle("POST /api/v1/libraries/{id}/folders", s.withAuth(allowAny, s.handleCreateFolder))
		mux.Handle("GET /api/v1/libraries/{id}/trash", s.withAuth(allowAny, s.handleListTrash))

		// Search.
		mux.Handle("GET /api/v1/libraries/{id}/search", s.withAuth(allowAny, s.handleSearch))

		// Tags.
		mux.Handle("GET /api/v1/libraries/{id}/tags", s.withAuth(allowAny, s.handleListTags))
		mux.Handle("POST /api/v1/libraries/{id}/tags", s.withAuth(allowAny, s.handleCreateTag))
		mux.Handle("DELETE /api/v1/libraries/{id}/tags/{tagID}", s.withAuth(allowAny, s.handleDeleteTag))
		mux.Handle("GET /api/v1/libraries/{id}/files/{fileID}/tags", s.withAuth(allowAny, s.handleListFileTags))
		mux.Handle("POST /api/v1/libraries/{id}/files/{fileID}/tags", s.withAuth(allowAny, s.handleAddFileTag))
		mux.Handle("DELETE /api/v1/libraries/{id}/files/{fileID}/tags/{tagID}", s.withAuth(allowAny, s.handleRemoveFileTag))

		// Albums.
		mux.Handle("GET /api/v1/libraries/{id}/albums", s.withAuth(allowAny, s.handleListAlbums))
		mux.Handle("POST /api/v1/libraries/{id}/albums", s.withAuth(allowAny, s.handleCreateAlbum))
		mux.Handle("PATCH /api/v1/libraries/{id}/albums/{albumID}", s.withAuth(allowAny, s.handleUpdateAlbum))
		mux.Handle("DELETE /api/v1/libraries/{id}/albums/{albumID}", s.withAuth(allowAny, s.handleDeleteAlbum))
		mux.Handle("GET /api/v1/libraries/{id}/albums/{albumID}/files", s.withAuth(allowAny, s.handleListAlbumFiles))
		mux.Handle("POST /api/v1/libraries/{id}/albums/{albumID}/files/{fileID}", s.withAuth(allowAny, s.handleAddAlbumFile))
		mux.Handle("DELETE /api/v1/libraries/{id}/albums/{albumID}/files/{fileID}", s.withAuth(allowAny, s.handleRemoveAlbumFile))

		// Favorites.
		mux.Handle("POST /api/v1/libraries/{id}/files/{fileID}/favorite", s.withAuth(allowAny, s.handleAddFavorite))
		mux.Handle("DELETE /api/v1/libraries/{id}/files/{fileID}/favorite", s.withAuth(allowAny, s.handleRemoveFavorite))
		mux.Handle("GET /api/v1/libraries/{id}/favorites", s.withAuth(allowAny, s.handleListFavorites))

		// Backups surface (admin). Backup policy is server-wide: where they
		// are written, when they run, and restore destinations. All serverside.
		if s.backups != nil {
			mux.Handle("POST /api/v1/backups", s.withAuth(allowAdmin, s.handleRunBackup))
			mux.Handle("GET /api/v1/backups", s.withAuth(allowAdmin, s.handleListBackups))
			mux.Handle("GET /api/v1/backups/{id}", s.withAuth(allowAdmin, s.handleGetBackup))
			mux.Handle("POST /api/v1/backups/{id}/verify", s.withAuth(allowAdmin, s.handleVerifyBackup))
			mux.Handle("POST /api/v1/backups/{id}/restore", s.withAuth(allowAdmin, s.handleRestoreBackup))
		}

		// Memories (Markdown documents with version history and references).
		mux.Handle("GET /api/v1/libraries/{id}/memories", s.withAuth(allowAny, s.handleListMemories))
		mux.Handle("POST /api/v1/libraries/{id}/memories", s.withAuth(allowAny, s.handleCreateMemory))
		mux.Handle("GET /api/v1/libraries/{id}/memories/{memoryID}", s.withAuth(allowAny, s.handleGetMemory))
		mux.Handle("PUT /api/v1/libraries/{id}/memories/{memoryID}", s.withAuth(allowAny, s.handleUpdateMemory))
		mux.Handle("DELETE /api/v1/libraries/{id}/memories/{memoryID}", s.withAuth(allowAny, s.handleDeleteMemory))
		mux.Handle("POST /api/v1/libraries/{id}/memories/{memoryID}/restore", s.withAuth(allowAny, s.handleRestoreMemory))
		mux.Handle("GET /api/v1/libraries/{id}/memories/{memoryID}/versions", s.withAuth(allowAny, s.handleListMemoryVersions))
		mux.Handle("GET /api/v1/libraries/{id}/memories/{memoryID}/versions/{version}", s.withAuth(allowAny, s.handleGetMemoryVersion))
		mux.Handle("GET /api/v1/libraries/{id}/memories/{memoryID}/refs", s.withAuth(allowAny, s.handleListMemoryRefs))
		mux.Handle("PATCH /api/v1/libraries/{id}/memories/{memoryID}", s.withAuth(allowAny, s.handlePatchMemory))
		mux.Handle("PUT /api/v1/libraries/{id}/memories/{memoryID}/document", s.withAuth(allowAny, s.handlePutMemoryDocument))
		mux.Handle("POST /api/v1/libraries/{id}/memories/{memoryID}/blocks", s.withAuth(allowAny, s.handleCreateMemoryBlock))
		mux.Handle("PUT /api/v1/libraries/{id}/memories/{memoryID}/blocks/order", s.withAuth(allowAny, s.handleReorderMemoryBlocks))
		mux.Handle("PATCH /api/v1/libraries/{id}/memories/{memoryID}/blocks/{blockID}", s.withAuth(allowAny, s.handlePatchMemoryBlock))
		mux.Handle("DELETE /api/v1/libraries/{id}/memories/{memoryID}/blocks/{blockID}", s.withAuth(allowAny, s.handleDeleteMemoryBlock))
		mux.Handle("POST /api/v1/libraries/{id}/memories/{memoryID}/blocks/{blockID}/duplicate", s.withAuth(allowAny, s.handleDuplicateMemoryBlock))
		mux.Handle("POST /api/v1/libraries/{id}/memories/{memoryID}/blocks/{blockID}/images", s.withAuth(allowAny, s.handleAddMemoryImages))
		mux.Handle("PUT /api/v1/libraries/{id}/memories/{memoryID}/blocks/{blockID}/images/order", s.withAuth(allowAny, s.handleReorderMemoryImages))
		mux.Handle("PATCH /api/v1/libraries/{id}/memories/{memoryID}/images/{imageID}", s.withAuth(allowAny, s.handlePatchMemoryImage))
		mux.Handle("DELETE /api/v1/libraries/{id}/memories/{memoryID}/images/{imageID}", s.withAuth(allowAny, s.handleDeleteMemoryImage))
		mux.Handle("GET /api/v1/libraries/{id}/memories/{memoryID}/images/{imageID}/derived", s.withAuth(allowAny, s.handleGetMemoryImageDerived))

		// Public shares read content without a session. The token/password
		// authenticate the share; capability evaluation still governs access.
		mux.HandleFunc("GET /api/v1/shares/{token}", s.handlePublicShareInfo)
		mux.HandleFunc("GET /api/v1/shares/{token}/files", s.handlePublicShareListFiles)
		mux.HandleFunc("GET /api/v1/shares/{token}/files/{fileID}", s.handlePublicShareGetFile)
		mux.HandleFunc("GET /api/v1/shares/{token}/files/{fileID}/download", s.handlePublicShareDownload)
		mux.HandleFunc("POST /api/v1/shares/{token}/authenticate", s.handlePublicShareAuthenticate)
	}

	mux.Handle("/api/", s.handleAPIUnknown())

	if s.web != nil {
		mux.Handle("/", s.web)
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			writeError(w, s.logger, requestIDOrEmpty(r), http.StatusNotFound, CodeNotFound,
				"No web frontend is configured. Build the frontend or point CAIRN_WEB_DIST at a built web/dist directory.")
		})
	}

	return WithMiddleware(s.logger, metricsRecording(s.metrics, mux))
}

// requireSession wraps a plain http.HandlerFunc with authentication. The
// session is authenticated and discarded; handlers that need the principal use
// withAuth instead.
func (s *Server) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.currentUser(w, r); !ok {
			return
		}
		next(w, r)
	}
}

// handleAPIUnknown returns a JSON 404 for any unmatched /api/ request,
// keeping API errors consistent regardless of the path.
func (s *Server) handleAPIUnknown() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeErrorf(w, s.logger, requestIDOrEmpty(r), http.StatusNotFound, CodeNotFound,
			"No such API route: %s %s.", r.Method, r.URL.Path)
	})
}
