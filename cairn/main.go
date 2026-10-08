// Command cairn runs the Cairn server: the JSON API, background workers, and
// the embedded web frontend, in a single process.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/audit"
	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
	"github.com/Jishnu-Prasad888/Cairn/internal/authz"
	"github.com/Jishnu-Prasad888/Cairn/internal/backups"
	"github.com/Jishnu-Prasad888/Cairn/internal/config"
	"github.com/Jishnu-Prasad888/Cairn/internal/crypto"
	"github.com/Jishnu-Prasad888/Cairn/internal/db"
	"github.com/Jishnu-Prasad888/Cairn/internal/httpapi"
	"github.com/Jishnu-Prasad888/Cairn/internal/indexer"
	"github.com/Jishnu-Prasad888/Cairn/internal/library"
	"github.com/Jishnu-Prasad888/Cairn/internal/logging"
	"github.com/Jishnu-Prasad888/Cairn/internal/metadata"
	"github.com/Jishnu-Prasad888/Cairn/internal/metrics"
	"github.com/Jishnu-Prasad888/Cairn/internal/ml"
	"github.com/Jishnu-Prasad888/Cairn/internal/version"
	"github.com/Jishnu-Prasad888/Cairn/internal/webui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "cairn:", err)
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
	logger.Info("starting cairn",
		"version", info.Version,
		"commit", info.Commit,
		"build_date", info.BuildDate,
		"go_version", info.GoVersion,
		"platform", info.Platform,
		"data_dir", cfg.DataDir,
		"http_addr", cfg.HTTPAddr,
	)

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
	// background job worker (index scans + media processing) once it is
	// registered; workers for already-registered libraries are started below.
	idxManager := indexer.NewIndexManager(logger)
	idxManager.SetBaseContext(ctx)
	mediaProc := metadata.NewProcessor(logger, keys)
	idxManager.SetMediaProcessor(mediaProc)

	// Recover persisted scan and media-processing jobs after a restart:
	// start one job worker per registered library. Offline libraries are
	// skipped and gain their worker on the next index trigger.
	if libs, err := libraries.List(ctx); err == nil {
		idxManager.StartWorkers(ctx, libs)
	} else {
		logger.Warn("list libraries for job workers", "error", err)
	}

	// Pick up files added or removed outside Cairn: rescan on a timer.
	idxManager.StartAutoScan(ctx, time.Duration(cfg.ScanIntervalSeconds)*time.Second,
		func(c context.Context) ([]library.Library, error) {
			if err := libraries.RefreshAll(c); err != nil {
				logger.Warn("refresh libraries before scan", "error", err)
			}
			return libraries.List(c)
		})

	// Local ML: optional similarity signatures. The manager is inert unless
	// CAIRN_ML_ENABLED is set; similarity passes run after index scans and on
	// explicit API calls.
	mlManager := ml.NewManager(logger, ml.Config{
		Enabled:           cfg.MLEnabled,
		Workers:           cfg.MLWorkers,
		DistanceThreshold: cfg.MLDistanceThreshold,
	}, ml.PerceptualHashProvider{})

	// Face recognition requires two models:
	//   1. SCRFD detector  (face-detector.onnx  / CAIRN_ML_FACE_DETECTOR_MODEL)
	//   2. ArcFace R50     (face-recognition.onnx / CAIRN_ML_FACE_MODEL)
	// Both must be present for face recognition to work. If either is missing,
	// the face manager is disabled and the app offers to download them.
	detectorPath := cfg.MLFaceDetectorModel
	if detectorPath == "" {
		detectorPath = filepath.Join(cfg.DataDir, "models", "face-detector.onnx")
	}
	recognizerPath := cfg.MLFaceModel
	if recognizerPath == "" {
		recognizerPath = filepath.Join(cfg.DataDir, "models", "face-recognition.onnx")
	}

	var faceProvider ml.FaceProvider
	detectorInstalled := fileExists(detectorPath)
	recognizerInstalled := fileExists(recognizerPath)
	switch {
	case detectorInstalled && recognizerInstalled:
		ep, err := ml.NewSCRFDEmbeddingFaceProvider(detectorPath, recognizerPath)
		if err != nil {
			logger.Error("face recognition models unusable; face recognition disabled — "+
				"re-download models from the ML settings page", "error", err)
		} else {
			faceProvider = ep
			logger.Info("SCRFD + ArcFace face recognition loaded",
				"detector", detectorPath, "recognizer", recognizerPath)
		}
	case detectorInstalled && !recognizerInstalled:
		logger.Warn("ArcFace recognizer not installed; face recognition disabled",
			"expected", recognizerPath)
	case !detectorInstalled && recognizerInstalled:
		logger.Warn("SCRFD detector not installed; face recognition disabled",
			"expected", detectorPath)
	default:
		logger.Warn("face recognition models not installed; the app offers to download them",
			"detector", detectorPath, "recognizer", recognizerPath)
	}

	faces := ml.NewFaceManager(logger, ml.FaceConfig{
		Enabled:       cfg.MLEnabled,
		Workers:       cfg.MLFaceWorkers,
		MinConfidence: cfg.MLFaceMinConfidence,
		MinSize:       cfg.MLFaceMinSize,
		Threshold:     cfg.MLFaceThreshold,
	}, faceProvider)

	// The master switch is an administrator setting stored in the server
	// database (Machine learning page); CAIRN_ML_ENABLED is only its default.
	var mlRuntime *ml.Runtime

	// Detector downloader: when the detector arrives, attempt to load both
	// models together (recognizer may also be present by then).
	detectorURL := cfg.MLFaceDetectorURL
	if detectorURL == "" {
		detectorURL = ml.DefaultFaceDetectorURL
	}
	faceDetector := ml.NewModelDownloader(
		logger, detectorPath, detectorURL,
		func(part string) error {
			_, err := ml.NewSCRFDDetector(part)
			return err
		},
		func(path string) {
			if !fileExists(recognizerPath) {
				return
			}
			prov, err := ml.NewSCRFDEmbeddingFaceProvider(path, recognizerPath)
			if err != nil {
				logger.Error("face provider load after detector install", "error", err)
				return
			}
			mlRuntime.ModelReady(prov)
		},
	)

	// Recognizer downloader.
	recognizerURL := cfg.MLFaceModelURL
	if recognizerURL == "" {
		recognizerURL = ml.DefaultFaceRecognizerURL
	}
	faceModel := ml.NewModelDownloader(
		logger, recognizerPath, recognizerURL,
		func(part string) error {
			if !fileExists(detectorPath) {
				return nil // detector not there yet; defer validation
			}
			_, err := ml.NewSCRFDEmbeddingFaceProvider(detectorPath, part)
			return err
		},
		func(path string) {
			if !fileExists(detectorPath) {
				logger.Info("ArcFace recognizer installed; waiting for SCRFD detector")
				return
			}
			prov, err := ml.NewSCRFDEmbeddingFaceProvider(detectorPath, path)
			if err != nil {
				logger.Error("face provider load after recognizer install", "error", err)
				return
			}
			mlRuntime.ModelReady(prov)
		},
	)

	mlRuntime = ml.NewRuntime(pool, logger, mlManager, faces, cfg.MLSimilarity,
		func(ctx context.Context) ([]ml.Target, error) {
			libs, err := libraries.List(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]ml.Target, 0, len(libs))
			for _, l := range libs {
				out = append(out, ml.Target{ID: l.ID, Root: l.Root, Online: l.Status == library.StatusOnline})
			}
			return out, nil
		})
	if err := mlRuntime.Load(ctx, cfg.MLEnabled); err != nil {
		return err
	}
	idxManager.AfterScan = mlRuntime.AfterScan
	if mlRuntime.Enabled() {
		// Catch up on anything added while the server was off.
		mlRuntime.StartAll()
	}

	// Backups: one-shot, scheduled, and restore operations over the server
	// database and every registered library. The scheduler is a no-op when no
	// backup directory is configured.
	backupMgr := backups.NewManager(pool, logger, libraries, backups.Config{
		Dir:        cfg.BackupDir,
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
		ML:             mlManager,
		Faces:          faces,
		MLRuntime:      mlRuntime,
		FaceModel:      faceModel,
		FaceDetector:   faceDetector,
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

// fileExists reports whether a file exists and is non-empty. Used to check
// whether model files have been downloaded before loading them.
func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Size() > 0
}
