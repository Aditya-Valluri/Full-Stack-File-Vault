package files

import (
	"context"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"file-vault.local/api/internal/auth"
)

// NewContentHandler is byte transport only. Grants are created through GraphQL.
// No physical key, owner ID, or filename in a URL can authorize a transfer.
func NewContentHandler(store *Store, storage ContentStore, logger *slog.Logger) http.Handler {
	return NewContentTransport("/content/", store.OpenAccess, storage, logger)
}

type transferGETKey struct{}

// IsDownloadGET is set only by byte transport, never from a client GraphQL input.
func IsDownloadGET(ctx context.Context) bool {
	value, _ := ctx.Value(transferGETKey{}).(bool)
	return value
}

// NewContentTransport shares restrictive response policy across personal and
// shared downloads. Each opener owns its independent authorization rules.
func NewContentTransport(prefix string, open func(context.Context, string, ContentStore) (OpenContent, error), storage ContentStore, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; frame-ancestors 'none'")
		if r.Method != "GET" && r.Method != "HEAD" {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", 405)
			return
		}
		if r.URL.RawQuery != "" || !strings.HasPrefix(r.URL.Path, prefix) {
			http.Error(w, "not found", 404)
			return
		}
		// ServeContent supports ranges, but multipart ranges are intentionally excluded.
		ranges := r.Header.Values("Range")
		if len(ranges) > 1 || (len(ranges) == 1 && (len(ranges[0]) > 128 || strings.Contains(ranges[0], ","))) {
			http.Error(w, "invalid range", 416)
			return
		}
		token := strings.TrimPrefix(r.URL.Path, prefix)
		requestCtx := context.WithValue(r.Context(), transferGETKey{}, r.Method == "GET")
		content, err := open(requestCtx, token, storage)
		if err != nil {
			var limited *auth.RateLimitError
			switch {
			case errors.As(err, &limited):
				w.Header().Set("Retry-After", strconv.Itoa(max(1, int((limited.RetryAfter+time.Second-1)/time.Second))))
				http.Error(w, "request rate exceeded", 429)
			case errors.Is(err, ErrNotFound), errors.Is(err, ErrPreviewUnsupported):
				http.Error(w, "not found", 404)
			case errors.Is(err, auth.ErrUnauthenticated):
				http.Error(w, "authentication required", 401)
			default:
				logger.Error("file transport unavailable", "code", "CONTENT_UNAVAILABLE")
				http.Error(w, "content unavailable", 503)
			}
			return
		}
		defer func() {
			if err := content.Body.Close(); err != nil {
				logger.Error("content handle close failed", "code", "CONTENT_CLOSE_FAILED")
			}
		}()
		disposition := "attachment"
		w.Header().Set("Content-Type", "application/octet-stream")
		if content.Mode == "PREVIEW" {
			disposition = "inline"
			w.Header().Set("Content-Type", content.File.DetectedMIME)
		}
		w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": content.File.Name}))
		// No hash ETag or Last-Modified validators that could disclose physical reuse.
		// Reapply security headers even when ServeContent strips cache headers on error.
		safe := &privateContentWriter{ResponseWriter: w}
		http.ServeContent(safe, r, content.File.Name, time.Time{}, content.Body)
	})
}

type privateContentWriter struct{ http.ResponseWriter }

func (w *privateContentWriter) WriteHeader(status int) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.ResponseWriter.WriteHeader(status)
}
func (w *privateContentWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
