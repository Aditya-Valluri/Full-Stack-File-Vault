// Package server provides operational HTTP endpoints and bounded shutdown.
package server

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"file-vault.local/api/internal/telemetry"
	"github.com/go-chi/chi/v5"
)

type Check func(context.Context) error

func New(addr string, ready Check, logger *slog.Logger, graphqlHandler http.Handler, contentHandlers ...http.Handler) *http.Server {
	if graphqlHandler == nil {
		panic("GraphQL handler is required")
	}
	r := chi.NewRouter()
	r.Use(telemetry.Default.Wrap)

	// Avoid logging request URLs or headers: future transport URLs can be secrets.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			defer func() {
				if recover() != nil {
					logger.Error("HTTP handler panic")
					http.Error(w, "internal server error", http.StatusInternalServerError)
				}
			}()
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			next.ServeHTTP(w, req)
		})
	})
	r.Get("/metrics", telemetry.Default.Handler().ServeHTTP)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"status\":\"ok\"}\n"))
	})
	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "application/json")
		if err := ready(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("{\"status\":\"unavailable\"}\n"))
			return
		}
		_, _ = w.Write([]byte("{\"status\":\"ready\"}\n"))
	})
	r.Handle("/graphql", graphqlHandler)
	if len(contentHandlers) > 0 && contentHandlers[0] != nil {
		r.Handle("/content/{token}", contentHandlers[0])
	}
	if len(contentHandlers) > 1 && contentHandlers[1] != nil {
		r.Handle("/shared-content/{token}", contentHandlers[1])
	}
	return &http.Server{
		Addr: addr, Handler: r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
}

// Serve drains in-flight requests before returning so callers can safely close
// their dependencies. Force-close connections if the shutdown deadline expires.
func Serve(ctx context.Context, srv *http.Server, listener net.Listener, timeout time.Duration) error {
	result := make(chan error, 1)
	go func() { result <- srv.Serve(listener) }()
	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			_ = srv.Close()
			<-result
			return err
		}
		err := <-result
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
