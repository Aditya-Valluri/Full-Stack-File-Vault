package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"file-vault.local/api/internal/admin"
	"file-vault.local/api/internal/auth"
	"file-vault.local/api/internal/blobstorage"
	"file-vault.local/api/internal/config"
	"file-vault.local/api/internal/database"
	"file-vault.local/api/internal/files"
	"file-vault.local/api/internal/graph"
	"file-vault.local/api/internal/server"
	"file-vault.local/api/internal/sharing"
	"file-vault.local/api/internal/telemetry"
	"file-vault.local/api/internal/upload"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("server stopped", "error", err.Error())
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	pool, err := database.Open(startupCtx, cfg.DatabaseURL)
	cancel()
	if err != nil {
		return err
	}
	defer pool.Close()
	metricsCtx, stopMetrics := context.WithCancel(ctx)
	defer stopMetrics()
	telemetry.Default.WatchDatabase(metricsCtx, pool)
	sessions, err := auth.NewSessionStore(pool, 24*time.Hour, 30*time.Minute)
	if err != nil {
		return err
	}
	browser, err := auth.NewBrowserSecurity(cfg.Browser, sessions)
	if err != nil {
		return err
	}
	// Only development creates directories automatically. Production storage mounts
	// must already exist and be private to the service account.
	if cfg.Browser.Development {
		for _, directory := range []string{cfg.Upload.Directory, cfg.BlobDirectory} {
			if err = os.MkdirAll(directory, 0700); err != nil {
				return errors.New("cannot initialize upload storage")
			}
		}
	}
	stagingRoot, err := os.OpenRoot(cfg.Upload.Directory)
	if err != nil {
		return errors.New("upload staging directory unavailable")
	}
	if err = stagingRoot.Close(); err != nil {
		return errors.New("upload staging directory unavailable")
	}
	blobs, err := blobstorage.Open(ctx, cfg.DatabaseURL, cfg.BlobDirectory, cfg.Browser.Development)
	if err != nil {
		return err
	}
	defer blobs.Close()
	publisher, err := upload.NewPublisher(pool, blobs, logger)
	if err != nil {
		return err
	}
	limiter, err := auth.NewUserLimiter(pool, cfg.UserCallsPerSecond)
	if err != nil {
		return err
	}
	reader, err := files.NewStore(pool)
	if err != nil {
		return err
	}
	shares, err := sharing.NewStore(pool)
	if err != nil {
		return err
	}
	administration, err := admin.NewStore(pool)
	if err != nil {
		return err
	}
	handler, err := graph.NewApplicationHandler(logger, browser, func(ctx context.Context, existing string) (string, auth.Session, bool, error) {
		return sessions.BeginSession(ctx, existing, cfg.BootstrapCreationsPerMinute)
	}, sessions.LoginBrowser, sessions.Revoke, publisher, reader, limiter, cfg.Upload, graph.Services{Sharing: shares, Administration: administration})
	if err != nil {
		return err
	}
	content := browser.WrapContent(limiter.Wrap(files.NewContentHandler(reader, blobs, logger)))
	srv := server.New(cfg.HTTPAddr, func(ctx context.Context) error { return database.Ready(ctx, pool) }, logger, handler, content, browser.WrapSharedContent(limiter.Wrap(files.NewContentTransport("/shared-content/", shares.OpenAccess, blobs, logger))))
	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return err
	}
	logger.Info("HTTP server listening", "address", listener.Addr().String())
	err = server.Serve(ctx, srv, listener, cfg.ShutdownTimeout)
	logger.Info("HTTP server stopped")
	return err
}
