package httpapi

import (
	"database/sql"
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/Jishnu-Prasad888/Orchard/internal/auth"
	"github.com/Jishnu-Prasad888/Orchard/internal/authz"
	"github.com/Jishnu-Prasad888/Orchard/internal/backups"
	"github.com/Jishnu-Prasad888/Orchard/internal/crypto"
	"github.com/Jishnu-Prasad888/Orchard/internal/indexer"
	"github.com/Jishnu-Prasad888/Orchard/internal/library"
	"github.com/Jishnu-Prasad888/Orchard/internal/metrics"
	"github.com/Jishnu-Prasad888/Orchard/internal/serversettings"
	"github.com/Jishnu-Prasad888/Orchard/internal/usersettings"
)

// Server is the Orchard HTTP application. It is a small composition of the
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
	backups        *backups.Manager
	keys           *crypto.Keys
	secureCookies  bool
	ratelimit      *rateLimiter
	maxUploadBytes int64
	metrics        *metrics.Registry
	settings       *serversettings.Store
	userSettings   *usersettings.Store
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
	// Backups runs server and library backups. It may be nil, in which case
	// the backup-management endpoints return SERVICE_UNAVAILABLE.
	Backups *backups.Manager
	// Keys is the optional at-rest encryption key used to seal the .orchard
	// identity file. It may be nil or disabled, in which case metadata is
	// written in the clear.
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
		backups:        deps.Backups,
		keys:           deps.Keys,
		secureCookies:  deps.SecureCookies,
		ratelimit:      newRateLimiter(),
		maxUploadBytes: deps.MaxUploadBytes,
		metrics:        deps.Metrics,
	}
	if deps.DB != nil {
		s.settings = serversettings.NewStore(deps.DB)
		s.userSettings = usersettings.NewStore(deps.DB)
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

	// Public: submitting an account request (gated at runtime by the
	// signup.requests_enabled server setting, checked inside the handler).
	mux.HandleFunc("POST /api/v1/auth/signup-requests", s.handleRequestSignup)

	// Admin surface.
	mux.Handle("GET /api/v1/users", s.withAuth(allowAdmin, s.handleListUsers))
	mux.Handle("POST /api/v1/users", s.withAuth(allowAdmin, s.handleCreateUser))
	mux.Handle("PATCH /api/v1/users/{id}", s.withAuth(allowAdmin, s.handleUpdateUser))
	mux.Handle("DELETE /api/v1/users/{id}", s.withAuth(allowAdmin, s.handleDeleteUser))
	mux.Handle("POST /api/v1/users/{id}/sessions/revoke", s.withAuth(allowAdmin, s.handleRevokeUserSessions))

	mux.Handle("GET /api/v1/signup-requests", s.withAuth(allowAdmin, s.handleListPendingSignups))
	mux.Handle("POST /api/v1/signup-requests/{id}/approve", s.withAuth(allowAdmin, s.handleApproveSignup))
	mux.Handle("POST /api/v1/signup-requests/{id}/deny", s.withAuth(allowAdmin, s.handleDenySignup))

	mux.Handle("PUT /api/v1/settings/signup-requests",
		s.withAuth(allowAdmin, s.handleSetSignupRequestsEnabled))

	// Reader preferences (font, spacing, theme) follow the user across
	// every library and every book — server-level, not per-library.
	mux.Handle("GET /api/v1/settings/reader", s.withAuth(allowAny, s.handleGetReaderPreferences))
	mux.Handle("PUT /api/v1/settings/reader", s.withAuth(allowAny, s.handleSetReaderPreferences))

	// Home dashboard layout (section order/visibility) is likewise a
	// server-level preference, not per-library: a user picks one layout for
	// Home regardless of which library is currently active.
	mux.Handle("GET /api/v1/settings/home-layout", s.withAuth(allowAny, s.handleGetHomeLayout))
	mux.Handle("PUT /api/v1/settings/home-layout", s.withAuth(allowAny, s.handleSetHomeLayout))

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

		// Permissions and shares administration (library scope).
		mux.Handle("GET /api/v1/libraries/{id}/permissions", s.withAuth(allowAny, s.handleListGrants))
		mux.Handle("POST /api/v1/libraries/{id}/permissions", s.withAuth(allowAny, s.handleCreateGrant))
		mux.Handle("DELETE /api/v1/libraries/{id}/permissions/{grantID}", s.withAuth(allowAny, s.handleRevokeGrant))
		mux.Handle("GET /api/v1/libraries/{id}/shares", s.withAuth(allowAny, s.handleListShares))
		mux.Handle("POST /api/v1/libraries/{id}/shares", s.withAuth(allowAny, s.handleCreateShare))
		mux.Handle("DELETE /api/v1/libraries/{id}/shares/{shareID}", s.withAuth(allowAny, s.handleRevokeShare))

		// Files surface. Capabilities are enforced per resource inside each
		// handler via requireCap.
		mux.Handle("GET /api/v1/libraries/{id}/files", s.withAuth(allowAny, s.handleListFiles))
		mux.Handle("GET /api/v1/libraries/{id}/files/counts", s.withAuth(allowAny, s.handleFileCounts))
		mux.Handle("GET /api/v1/libraries/{id}/files/{fileID}", s.withAuth(allowAny, s.handleGetFile))
		mux.Handle("GET /api/v1/libraries/{id}/files/{fileID}/download", s.withAuth(allowAny, s.handleDownloadFile))
		mux.Handle("POST /api/v1/libraries/{id}/files/upload", s.withAuth(allowAny, s.handleUploadFile))
		mux.Handle("POST /api/v1/libraries/{id}/files/{fileID}/rename", s.withAuth(allowAny, s.handleRenameFile))
		mux.Handle("POST /api/v1/libraries/{id}/files/{fileID}/move", s.withAuth(allowAny, s.handleMoveFile))
		mux.Handle("POST /api/v1/libraries/{id}/files/{fileID}/copy", s.withAuth(allowAny, s.handleCopyFile))
		mux.Handle("DELETE /api/v1/libraries/{id}/files/{fileID}", s.withAuth(allowAny, s.handleDeleteFile))
		mux.Handle("POST /api/v1/libraries/{id}/files/{fileID}/restore", s.withAuth(allowAny, s.handleRestoreFile))
		mux.Handle("DELETE /api/v1/libraries/{id}/files/{fileID}/permanent", s.withAuth(allowAny, s.handlePermanentDeleteFile))

		// Per-file Markdown notes (captions).
		mux.Handle("GET /api/v1/libraries/{id}/files/{fileID}/note", s.withAuth(allowAny, s.handleGetFileNote))
		mux.Handle("PUT /api/v1/libraries/{id}/files/{fileID}/note", s.withAuth(allowAny, s.handleSetFileNote))
		mux.Handle("DELETE /api/v1/libraries/{id}/files/{fileID}/note", s.withAuth(allowAny, s.handleClearFileNote))

		// Book metadata, cover, and reading status (library management).
		mux.Handle("GET /api/v1/libraries/{id}/files/{fileID}/book-metadata",
			s.withAuth(allowAny, s.handleGetBookMetadata))
		mux.Handle("PATCH /api/v1/libraries/{id}/files/{fileID}/book-metadata",
			s.withAuth(allowAny, s.handleUpdateBookMetadata))
		mux.Handle("GET /api/v1/libraries/{id}/files/{fileID}/cover", s.withAuth(allowAny, s.handleGetCover))
		mux.Handle("PUT /api/v1/libraries/{id}/files/{fileID}/cover", s.withAuth(allowAny, s.handleUploadCover))
		mux.Handle("GET /api/v1/libraries/{id}/files/{fileID}/reading-status",
			s.withAuth(allowAny, s.handleGetReadingStatus))
		mux.Handle("PUT /api/v1/libraries/{id}/files/{fileID}/reading-status",
			s.withAuth(allowAny, s.handleSetReadingStatus))
		mux.Handle("DELETE /api/v1/libraries/{id}/files/{fileID}/reading-status",
			s.withAuth(allowAny, s.handleClearReadingStatus))
		mux.Handle("GET /api/v1/libraries/{id}/files/{fileID}/progress",
			s.withAuth(allowAny, s.handleGetReadingProgress))
		mux.Handle("PUT /api/v1/libraries/{id}/files/{fileID}/progress",
			s.withAuth(allowAny, s.handleSetReadingProgress))
		mux.Handle("DELETE /api/v1/libraries/{id}/files/{fileID}/progress",
			s.withAuth(allowAny, s.handleClearReadingProgress))
		mux.Handle("GET /api/v1/libraries/{id}/reading/in-progress",
			s.withAuth(allowAny, s.handleListInProgress))

		// Recently opened (per-user "last opened" tracking) and duplicate
		// detection. The literal "recently-opened"/"duplicates" segments take
		// precedence over the GET .../files/{fileID} route above regardless
		// of registration order — ServeMux always prefers the more specific
		// pattern (the same rule "files/counts" already relies on).
		mux.Handle("GET /api/v1/libraries/{id}/files/recently-opened",
			s.withAuth(allowAny, s.handleListRecentlyOpened))
		mux.Handle("POST /api/v1/libraries/{id}/files/{fileID}/opened",
			s.withAuth(allowAny, s.handleMarkFileOpened))
		mux.Handle("GET /api/v1/libraries/{id}/files/duplicates", s.withAuth(allowAny, s.handleListDuplicates))

		// Folders and trash.
		mux.Handle("GET /api/v1/libraries/{id}/folders", s.withAuth(allowAny, s.handleListFolders))
		mux.Handle("POST /api/v1/libraries/{id}/folders", s.withAuth(allowAny, s.handleCreateFolder))
		mux.Handle("GET /api/v1/libraries/{id}/trash", s.withAuth(allowAny, s.handleListTrash))

		// Search.
		mux.Handle("GET /api/v1/libraries/{id}/search", s.withAuth(allowAny, s.handleSearch))
		// Content search: extracted book text, metadata, quotes,
		// highlights, and notes — as opposed to the path-only search above.
		mux.Handle("GET /api/v1/libraries/{id}/search/content", s.withAuth(allowAny, s.handleSearchContent))
		mux.Handle("GET /api/v1/libraries/{id}/files/{fileID}/search/content",
			s.withAuth(allowAny, s.handleSearchFileContent))
		// Per-user recent searches.
		mux.Handle("GET /api/v1/libraries/{id}/search/recent", s.withAuth(allowAny, s.handleListRecentSearches))
		mux.Handle("POST /api/v1/libraries/{id}/search/recent", s.withAuth(allowAny, s.handleRecordRecentSearch))
		mux.Handle("DELETE /api/v1/libraries/{id}/search/recent", s.withAuth(allowAny, s.handleForgetRecentSearch))

		// Tags.
		mux.Handle("GET /api/v1/libraries/{id}/tags", s.withAuth(allowAny, s.handleListTags))
		mux.Handle("POST /api/v1/libraries/{id}/tags", s.withAuth(allowAny, s.handleCreateTag))
		mux.Handle("DELETE /api/v1/libraries/{id}/tags/{tagID}", s.withAuth(allowAny, s.handleDeleteTag))
		mux.Handle("GET /api/v1/libraries/{id}/files/{fileID}/tags", s.withAuth(allowAny, s.handleListFileTags))
		mux.Handle("POST /api/v1/libraries/{id}/files/{fileID}/tags", s.withAuth(allowAny, s.handleAddFileTag))
		mux.Handle("DELETE /api/v1/libraries/{id}/files/{fileID}/tags/{tagID}", s.withAuth(allowAny, s.handleRemoveFileTag))

		// Favorites.
		mux.Handle("POST /api/v1/libraries/{id}/files/{fileID}/favorite", s.withAuth(allowAny, s.handleAddFavorite))
		mux.Handle("DELETE /api/v1/libraries/{id}/files/{fileID}/favorite", s.withAuth(allowAny, s.handleRemoveFavorite))
		mux.Handle("GET /api/v1/libraries/{id}/favorites", s.withAuth(allowAny, s.handleListFavorites))

		// Collections. Personal, user-owned groupings of files — unlike
		// tags, scoped by the caller's own user ID, never shared.
		mux.Handle("GET /api/v1/libraries/{id}/collections", s.withAuth(allowAny, s.handleListCollections))
		mux.Handle("POST /api/v1/libraries/{id}/collections", s.withAuth(allowAny, s.handleCreateCollection))
		mux.Handle("PATCH /api/v1/libraries/{id}/collections/{collectionID}", s.withAuth(allowAny, s.handleUpdateCollection))
		mux.Handle("DELETE /api/v1/libraries/{id}/collections/{collectionID}", s.withAuth(allowAny, s.handleDeleteCollection))
		mux.Handle("GET /api/v1/libraries/{id}/collections/{collectionID}/files", s.withAuth(allowAny, s.handleListCollectionFiles))
		mux.Handle("POST /api/v1/libraries/{id}/collections/{collectionID}/files", s.withAuth(allowAny, s.handleAddCollectionFile))
		mux.Handle("DELETE /api/v1/libraries/{id}/collections/{collectionID}/files/{fileID}", s.withAuth(allowAny, s.handleRemoveCollectionFile))
		mux.Handle("GET /api/v1/libraries/{id}/files/{fileID}/collections", s.withAuth(allowAny, s.handleListFileCollections))

		// Bookmarks. Many per (user, file); each has its own id.
		mux.Handle("GET /api/v1/libraries/{id}/bookmarks", s.withAuth(allowAny, s.handleListLibraryBookmarks))
		mux.Handle("GET /api/v1/libraries/{id}/files/{fileID}/bookmarks", s.withAuth(allowAny, s.handleListFileBookmarks))
		mux.Handle("POST /api/v1/libraries/{id}/files/{fileID}/bookmarks", s.withAuth(allowAny, s.handleCreateBookmark))
		mux.Handle("PATCH /api/v1/libraries/{id}/bookmarks/{bookmarkID}", s.withAuth(allowAny, s.handleUpdateBookmark))
		mux.Handle("DELETE /api/v1/libraries/{id}/bookmarks/{bookmarkID}", s.withAuth(allowAny, s.handleDeleteBookmark))

		// Highlights. A selected passage of text plus an optional note/color.
		mux.Handle("GET /api/v1/libraries/{id}/highlights", s.withAuth(allowAny, s.handleListLibraryHighlights))
		mux.Handle("GET /api/v1/libraries/{id}/files/{fileID}/highlights", s.withAuth(allowAny, s.handleListFileHighlights))
		mux.Handle("POST /api/v1/libraries/{id}/files/{fileID}/highlights", s.withAuth(allowAny, s.handleCreateHighlight))
		mux.Handle("PATCH /api/v1/libraries/{id}/highlights/{highlightID}", s.withAuth(allowAny, s.handleUpdateHighlight))
		mux.Handle("DELETE /api/v1/libraries/{id}/highlights/{highlightID}", s.withAuth(allowAny, s.handleDeleteHighlight))

		// Quotes. Saved passages with citation details and commentary,
		// independent of highlights (can be typed in by hand).
		mux.Handle("GET /api/v1/libraries/{id}/quotes", s.withAuth(allowAny, s.handleListLibraryQuotes))
		mux.Handle("GET /api/v1/libraries/{id}/files/{fileID}/quotes", s.withAuth(allowAny, s.handleListFileQuotes))
		mux.Handle("POST /api/v1/libraries/{id}/files/{fileID}/quotes", s.withAuth(allowAny, s.handleCreateQuote))
		mux.Handle("PATCH /api/v1/libraries/{id}/quotes/{quoteID}", s.withAuth(allowAny, s.handleUpdateQuote))
		mux.Handle("DELETE /api/v1/libraries/{id}/quotes/{quoteID}", s.withAuth(allowAny, s.handleDeleteQuote))

		// Notes. Long-form per-user Markdown writing: many notes per file,
		// or a standalone note with no file/quote/highlight link at all.
		// Distinct from the single-caption GET/PUT/DELETE .../note above.
		mux.Handle("GET /api/v1/libraries/{id}/notes", s.withAuth(allowAny, s.handleListNotes))
		mux.Handle("POST /api/v1/libraries/{id}/notes", s.withAuth(allowAny, s.handleCreateNote))
		mux.Handle("GET /api/v1/libraries/{id}/files/{fileID}/notes", s.withAuth(allowAny, s.handleListFileNotes))
		mux.Handle("GET /api/v1/libraries/{id}/notes/{noteID}", s.withAuth(allowAny, s.handleGetNote))
		mux.Handle("PATCH /api/v1/libraries/{id}/notes/{noteID}", s.withAuth(allowAny, s.handleUpdateNote))
		mux.Handle("DELETE /api/v1/libraries/{id}/notes/{noteID}", s.withAuth(allowAny, s.handleDeleteNote))
		mux.Handle("GET /api/v1/libraries/{id}/notes/{noteID}/tags", s.withAuth(allowAny, s.handleListNoteTags))
		mux.Handle("POST /api/v1/libraries/{id}/notes/{noteID}/tags", s.withAuth(allowAny, s.handleAddNoteTag))
		mux.Handle("DELETE /api/v1/libraries/{id}/notes/{noteID}/tags/{tagID}", s.withAuth(allowAny, s.handleRemoveNoteTag))
		mux.Handle("GET /api/v1/libraries/{id}/notes/{noteID}/links", s.withAuth(allowAny, s.handleListNoteLinks))
		mux.Handle("POST /api/v1/libraries/{id}/notes/{noteID}/links", s.withAuth(allowAny, s.handleAddNoteLink))
		mux.Handle("DELETE /api/v1/libraries/{id}/notes/{noteID}/links/{linkedNoteID}", s.withAuth(allowAny, s.handleRemoveNoteLink))

		// Backups surface (admin). Backup policy is server-wide: where they
		// are written, when they run, and restore destinations. All serverside.
		if s.backups != nil {
			mux.Handle("POST /api/v1/backups", s.withAuth(allowAdmin, s.handleRunBackup))
			mux.Handle("GET /api/v1/backups", s.withAuth(allowAdmin, s.handleListBackups))
			mux.Handle("GET /api/v1/backups/{id}", s.withAuth(allowAdmin, s.handleGetBackup))
			mux.Handle("POST /api/v1/backups/{id}/verify", s.withAuth(allowAdmin, s.handleVerifyBackup))
			mux.Handle("POST /api/v1/backups/{id}/restore", s.withAuth(allowAdmin, s.handleRestoreBackup))
		}

		// Book metadata export is independent of the backup manager being
		// configured (destination, encryption key, …) — it only reads each
		// library's own database — so it is registered unconditionally.
		mux.Handle("GET /api/v1/backups/export/book-metadata.csv",
			s.withAuth(allowAdmin, s.handleExportBookMetadata))

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
				"No web frontend is configured. Build the frontend or point ORCHARD_WEB_DIST at a built web/dist directory.")
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
