// Package telemetry exposes aggregate operational metrics with fixed labels only.
package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
)

var Default = New()
var routes = []string{"graphql", "content", "shared_content", "other"}
var bounds = []float64{0.1, 0.5, 1, 5, 30}

type routeStats struct {
	responses [6]uint64
	buckets   [6]uint64
	count     uint64
	seconds   float64
}
type Registry struct {
	mu                                                    sync.Mutex
	started                                               time.Time
	http                                                  [4]routeStats
	errors                                                map[string]uint64
	cleanupAttempt, cleanupSuccess                        int64
	cleanupFailures, temporaryRemoved, generationsRemoved uint64
	databaseAvailable                                     bool
	databaseUpdated                                       int64
	pendingBlobs, pendingBytes, tombstones, receiptBytes  float64
}

func New() *Registry { return &Registry{started: time.Now(), errors: map[string]uint64{}} }
func (r *Registry) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/healthz" || req.URL.Path == "/readyz" || req.URL.Path == "/metrics" {
			next.ServeHTTP(w, req)
			return
		}
		route := 3
		switch {
		case req.URL.Path == "/graphql":
			route = 0
		case strings.HasPrefix(req.URL.Path, "/content/"):
			route = 1
		case strings.HasPrefix(req.URL.Path, "/shared-content/"):
			route = 2
		}
		start := time.Now()
		response := chimiddleware.NewWrapResponseWriter(w, req.ProtoMajor)
		defer func() {
			status := response.Status()
			if status == 0 {
				status = 200
			}
			class := status / 100
			if class < 1 || class > 5 {
				class = 5
			}
			elapsed := time.Since(start).Seconds()
			r.mu.Lock()
			defer r.mu.Unlock()
			item := &r.http[route]
			item.responses[class]++
			item.count++
			item.seconds += elapsed
			for i, bound := range bounds {
				if elapsed <= bound {
					item.buckets[i]++
				}
			}
			item.buckets[5]++
		}()
		next.ServeHTTP(response, req)
	})
}
func (r *Registry) GraphQLError(code string) {
	switch code {
	case "UNAUTHENTICATED", "FORBIDDEN", "RATE_LIMITED", "QUOTA_EXCEEDED", "INVALID_INPUT", "NOT_FOUND", "CONFLICT", "UPLOAD_RETRY_CONFLICT", "INTERNAL_ERROR", "UNSUPPORTED_MEDIA_TYPE", "REQUEST_TOO_LARGE":
	default:
		code = "OTHER"
	}
	r.mu.Lock()
	r.errors[code]++
	r.mu.Unlock()
}
func (r *Registry) Cleanup(removed, temporary int, failed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanupAttempt = time.Now().Unix()
	r.generationsRemoved += uint64(removed)
	r.temporaryRemoved += uint64(temporary)
	if failed {
		r.cleanupFailures++
	} else {
		r.cleanupSuccess = r.cleanupAttempt
	}
}

// WatchDatabase samples bounded aggregate queries once per minute. Scrapes never
// trigger database work or expose identifiers; a failed sample is marked unavailable.
func (r *Registry) WatchDatabase(ctx context.Context, pool *pgxpool.Pool) {
	go func() {
		timer := time.NewTimer(0)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			var count, bytes, tombstones, receipts float64
			err := pool.QueryRow(queryCtx, `SELECT
    (SELECT count(*) FROM vault.blobs WHERE state='GC_PENDING'),
    (SELECT COALESCE(sum(size_bytes),0) FROM vault.blobs WHERE state='GC_PENDING'),
    (SELECT count(*) FROM vault.object_candidates WHERE state='DELETING'),
    pg_total_relation_size('vault.upload_receipts')`).Scan(&count, &bytes, &tombstones, &receipts)
			cancel()
			r.mu.Lock()
			r.databaseAvailable = err == nil
			if err == nil {
				r.databaseUpdated = time.Now().Unix()
				r.pendingBlobs = count
				r.pendingBytes = bytes
				r.tombstones = tombstones
				r.receiptBytes = receipts
			}
			r.mu.Unlock()
			timer.Reset(time.Minute)
		}
	}()
}

func (r *Registry) snapshot() *Registry {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := &Registry{
		started: r.started, http: r.http, errors: map[string]uint64{},
		cleanupAttempt: r.cleanupAttempt, cleanupSuccess: r.cleanupSuccess,
		cleanupFailures: r.cleanupFailures, temporaryRemoved: r.temporaryRemoved, generationsRemoved: r.generationsRemoved,
		databaseAvailable: r.databaseAvailable, databaseUpdated: r.databaseUpdated,
		pendingBlobs: r.pendingBlobs, pendingBytes: r.pendingBytes, tombstones: r.tombstones, receiptBytes: r.receiptBytes,
	}
	for code, count := range r.errors {
		result.errors[code] = count
	}
	return result
}
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		state := r.snapshot()
		fmt.Fprintln(w, "# TYPE vault_http_requests_total counter")
		fmt.Fprintln(w, "# TYPE vault_http_duration_seconds histogram")
		for i, route := range routes {
			stats := state.http[i]
			for class := 1; class <= 5; class++ {
				fmt.Fprintf(w, "vault_http_requests_total{route=%q,status_class=%q} %d\n", route, fmt.Sprintf("%dxx", class), stats.responses[class])
			}
			for j, bound := range bounds {
				fmt.Fprintf(w, "vault_http_duration_seconds_bucket{route=%q,le=%q} %d\n", route, fmt.Sprint(bound), stats.buckets[j])
			}
			fmt.Fprintf(w, "vault_http_duration_seconds_bucket{route=%q,le=\"+Inf\"} %d\n", route, stats.buckets[5])
			fmt.Fprintf(w, "vault_http_duration_seconds_sum{route=%q} %g\nvault_http_duration_seconds_count{route=%q} %d\n", route, stats.seconds, route, stats.count)
		}
		fmt.Fprintln(w, "# TYPE vault_graphql_errors_total counter")
		for code, count := range state.errors {
			fmt.Fprintf(w, "vault_graphql_errors_total{code=%q} %d\n", code, count)
		}
		fmt.Fprintf(w, "vault_process_start_time_seconds %d\n", state.started.Unix())
		fmt.Fprintf(w, "vault_go_goroutines %d\n", runtime.NumGoroutine())
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		fmt.Fprintf(w, "vault_go_heap_bytes %d\n", memory.HeapAlloc)
		fmt.Fprintf(w, "vault_cleanup_last_attempt_seconds %d\nvault_cleanup_last_success_seconds %d\nvault_cleanup_failures_total %d\nvault_cleanup_removals_completed_total %d\nvault_cleanup_temporary_removed_total %d\n", state.cleanupAttempt, state.cleanupSuccess, state.cleanupFailures, state.generationsRemoved, state.temporaryRemoved)
		available := 0
		if state.databaseAvailable {
			available = 1
		}
		fmt.Fprintf(w, "vault_database_metrics_available %d\nvault_database_metrics_updated_seconds %d\nvault_gc_pending_blobs %g\nvault_gc_pending_bytes %g\nvault_cleanup_tombstones %g\nvault_upload_receipt_storage_bytes %g\n", available, state.databaseUpdated, state.pendingBlobs, state.pendingBytes, state.tombstones, state.receiptBytes)
	})
}
