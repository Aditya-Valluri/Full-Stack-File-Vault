// Command collect runs the separately privileged exact-generation cleanup worker.
package main

import (
	"context"
	"errors"
	"flag"
	"full-stack-file-vault.local/api/internal/telemetry"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"full-stack-file-vault.local/api/internal/blobstorage"
	"full-stack-file-vault.local/api/internal/cleanup"
	"full-stack-file-vault.local/api/internal/secret"
	"full-stack-file-vault.local/api/internal/upload"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("cleanup worker stopped", "code", "CLEANUP_FAILED")
		os.Exit(1)
	}
}
func run(logger *slog.Logger) error {
	once := flag.Bool("once", false, "run one bounded cleanup cycle")
	batch := flag.Int("batch", 20, "maximum items per cleanup phase, 1..100")
	interval := flag.Duration("interval", 30*time.Second, "delay between cycles, 1s..1h")
	flag.Parse()
	if flag.NArg() != 0 || *batch < 1 || *batch > 100 || *interval < time.Second || *interval > time.Hour {
		return errors.New("invalid cleanup settings")
	}
	dsn, err := secret.Read("GC_DATABASE_URL")
	if err != nil {
		return err
	}
	directory := os.Getenv("BLOB_STORAGE_DIR")
	staging := os.Getenv("UPLOAD_STAGING_DIR")
	environment := os.Getenv("APP_ENV")
	if environment == "" {
		environment = "production"
	}
	if dsn == "" || directory == "" || (environment != "production" && environment != "development") {
		return errors.New("explicit GC database, blob directory, and valid environment required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return err
	}
	config.MaxConns = 2
	config.ConnConfig.ConnectTimeout = 5 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return err
	}
	defer pool.Close()
	// Prevent accidentally running the worker under an administrator/runtime login.
	check, cancel := context.WithTimeout(ctx, 5*time.Second)
	var role string
	err = pool.QueryRow(check, "SELECT current_user").Scan(&role)
	cancel()
	if err != nil || role != "vault_gc" {
		return errors.New("cleanup requires the vault_gc role")
	}
	if staging == "" && environment == "development" {
		staging = "data/staging"
	}
	if staging == "" {
		return errors.New("explicit upload staging directory required")
	}
	// Match the API's local-development bootstrap. Production mounts must exist.
	if environment == "development" {
		if err := os.MkdirAll(staging, 0700); err != nil {
			return errors.New("cannot initialize staging storage")
		}
		if err := os.MkdirAll(directory, 0700); err != nil {
			return errors.New("cannot initialize cleanup storage")
		}
	}
	storage, err := blobstorage.Open(ctx, dsn, directory, environment == "development")
	if err != nil {
		return err
	}
	defer storage.Close()
	temporary, err := upload.NewTemporaryCleaner(staging, directory, *batch)
	if err != nil {
		return err
	}
	defer temporary.Close()
	collector, err := cleanup.NewCollector(pool, storage, *batch, temporary)
	if err != nil {
		return err
	}
	if *once {
		report, err := collector.RunOnce(ctx)
		if err == nil {
			logger.Info("cleanup cycle", "retired", report.Retired, "removed", report.Removed, "grants_pruned", report.GrantsPruned, "temporary_removed", report.TemporaryRemoved)
		}
		return err
	}
	address := os.Getenv("GC_METRICS_ADDR")
	if address == "" {
		address = "127.0.0.1:8082"
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", telemetry.Default.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	metrics := &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 3 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return errors.New("cleanup metrics listener unavailable")
	}
	defer metrics.Close()
	go func() {
		if err := metrics.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("cleanup metrics unavailable", "code", "METRICS_FAILED")
		}
	}()
	return collector.Run(ctx, *interval, logger)
}
