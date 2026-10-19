// Command orchard runs the Orchard server: the JSON API, background workers,
// and the embedded web frontend, in a single process.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/audit"
	"github.com/Jishnu-Prasad888/Orchard/internal/auth"
	"github.com/Jishnu-Prasad888/Orchard/internal/authz"
	"github.com/Jishnu-Prasad888/Orchard/internal/backups"
	"github.com/Jishnu-Prasad888/Orchard/internal/bookmeta"
	"github.com/Jishnu-Prasad888/Orchard/internal/config"
	"github.com/Jishnu-Prasad888/Orchard/internal/crypto"
	"github.com/Jishnu-Prasad888/Orchard/internal/db"
	"github.com/Jishnu-Prasad888/Orchard/internal/httpapi"
	"github.com/Jishnu-Prasad888/Orchard/internal/indexer"
	"github.com/Jishnu-Prasad888/Orchard/internal/library"
	"github.com/Jishnu-Prasad888/Orchard/internal/logging"
	"github.com/Jishnu-Prasad888/Orchard/internal/memlimit"
	"github.com/Jishnu-Prasad888/Orchard/internal/metrics"
	"github.com/Jishnu-Prasad888/Orchard/internal/serversettings"
	"github.com/Jishnu-Prasad888/Orchard/internal/thumbs"
	"github.com/Jishnu-Prasad888/Orchard/internal/version"
	"github.com/Jishnu-Prasad888/Orchard/internal/webui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "orchard:", err)
		os.Exit(1)
	}
}

// run wires the application together and blocks until the server shuts down.
// It is separated from main so shutdown paths can be exercised and so logging
// stays off stdout.
func run() error {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	logger := logging.New(cfg.LogLevel)
	info := version.Get()
	logger.Info("starting orchard",
		"version", info.Version,
		"commit", info.Commit,
		"build_date", info.BuildDate,
		"go_version", info.GoVersion,
		"platform", info.Platform,
		"data_dir", cfg.DataDir,
		"http_addr", cfg.HTTPAddr,
	)
	memlimit.Apply(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Server-level database.
	pool, err := db.Open(cfg.DatabasePath())
	if err != nil {
		return err
	}
	defer func() { _ = pool.Close() }()

	if err := db.Migrate(pool); err != nil {
		return fmt.Errorf("apply database migrations: %w", err)
	}

	// Audit and authentication from the server database.
	auditSvc := audit.New(pool, logger)
	authSvc := auth.NewService(pool, logger, auditSvc)
	pruneCtx, cancelPrune := context.WithTimeout(ctx, 15*time.Second)
	if err := authSvc.PruneExpired(pruneCtx, time.Now().UTC()); err != nil {
		cancelPrune()
		return err
	}
	cancelPrune()

	// Resource-based authorization (ADR-0005): grants and public shares over
	// registered libraries. Backed by the server database and audit log.
	authzSvc := authz.NewService(pool, logger, auditSvc)

	// Registered storage libraries; reconcile connectivity at startup so a
	// disconnected disk is surfaced as offline immediately.
	keys := crypto.NewKeys(cfg.EncryptionPassphrase)
	if keys.Enabled() {
		logger.Info("at-rest metadata encryption enabled")
	}
	libraries := library.NewManager(pool, logger, auditSvc, keys)
	refreshCtx, cancelRefresh := context.WithTimeout(ctx, 15*time.Second)
	if err := libraries.RefreshAll(refreshCtx); err != nil {
		cancelRefresh()
		return err
	}
	cancelRefresh()

	// Incremental indexer: manages per-library scan jobs. Each library gets a
	// background job worker (index scans + file processing) once it is
	// registered; workers for already-registered libraries are started below.
	// bookmeta.Processor extracts book metadata/covers/text for document
	// files via the indexer.MediaProcessor extension point.
	idxManager := indexer.NewIndexManager(logger)
	idxManager.SetBaseContext(ctx)
	// Keep the compiled PDF renderer between runs (verified before each use).
	bookmeta.SetPDFRendererCache(filepath.Join(cfg.DataDir, "cache"), logger)
	idxManager.SetMediaProcessor(indexer.Combine(bookmeta.NewProcessor(logger), thumbs.NewProcessor(logger)))

	// Recover persisted scan and processing jobs after a restart: start one
	// job worker per registered library. Offline libraries are skipped and
	// gain their worker on the next index trigger.
	if libs, err := libraries.List(ctx); err == nil {
		idxManager.StartWorkers(ctx, libs)
	} else {
		logger.Warn("list libraries for job workers", "error", err)
	}

	// Pick up files added or removed outside Orchard: rescan on a timer.
	idxManager.StartAutoScan(ctx, time.Duration(cfg.ScanIntervalSeconds)*time.Second,
		func(c context.Context) ([]library.Library, error) {
			if err := libraries.RefreshAll(c); err != nil {
				logger.Warn("refresh libraries before scan", "error", err)
			}
			return libraries.List(c)
		})

	// Backups: one-shot, scheduled, and restore operations over the server
	// database and every registered library. The scheduler is a no-op when no
	// backup directory is configured.
	backupDir, backupDirSource := resolveBackupDir(ctx, cfg, serversettings.NewStore(pool), logger)
	backupMgr := backups.NewManager(pool, logger, libraries, backups.Config{
		Dir:        backupDir,
		DirSource:  backupDirSource,
		Keep:       cfg.BackupKeep,
		Passphrase: cfg.BackupPassphrase,
		Interval:   time.Duration(cfg.BackupIntervalMinutes) * time.Minute,
	}, cfg.DatabasePath())
	backupMgr.Start(ctx)

	// Frontend: the embedded build by default, an on-disk build in development.
	webHandler, err := webui.Handler(cfg.WebDistDir)
	if err != nil {
		logger.Warn("frontend unavailable", "error", err)
		webHandler = nil
	}

	api := httpapi.New(httpapi.Dependencies{
		Logger:         logger,
		DB:             pool,
		Auth:           authSvc,
		Authz:          authzSvc,
		Libraries:      libraries,
		Indexer:        idxManager,
		Backups:        backupMgr,
		Keys:           keys,
		SecureCookies:  cfg.CookieSecure,
		MaxUploadBytes: cfg.MaxUploadBytes,
		WebUI:          webHandler,
		Metrics:        metrics.New(),
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", cfg.HTTPAddr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// Flip /ready to 503 before draining so proxies and orchestrators stop
	// sending new traffic while in-flight requests finish.
	api.MarkNotReady()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	logger.Info("server stopped")
	return nil
}

// resolveBackupDir picks where backups go: the folder an administrator chose
// in the app, else ORCHARD_BACKUP_DIR, else "backups" inside the data
// directory — so backups work out of the box without any configuration.
func resolveBackupDir(
	ctx context.Context, cfg config.Config, settings *serversettings.Store, logger *slog.Logger,
) (dir, source string) {
	saved, err := settings.GetString(ctx, backups.DirSettingKey, "")
	if err != nil {
		logger.Warn("read saved backup directory", "error", err)
	}
	switch {
	case saved != "":
		return saved, backups.DirFromSetting
	case cfg.BackupDir != "":
		return cfg.BackupDir, backups.DirFromEnv
	default:
		return filepath.Join(cfg.DataDir, "backups"), backups.DirFromDefault
	}
}
