package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"balkanid.local/vault/api/internal/config"
	"balkanid.local/vault/api/internal/database"
	"balkanid.local/vault/api/internal/server"
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
	srv := server.New(cfg.HTTPAddr, func(ctx context.Context) error { return database.Ready(ctx, pool) }, logger)
	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return err
	}
	logger.Info("HTTP server listening", "address", listener.Addr().String())
	err = server.Serve(ctx, srv, listener, cfg.ShutdownTimeout)
	logger.Info("HTTP server stopped")
	return err
}
