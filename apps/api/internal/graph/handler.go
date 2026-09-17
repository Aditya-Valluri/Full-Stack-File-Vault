package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

const MaxRequestBytes int64 = 64 << 10

// NewHandler explicitly enables JSON POST only. Multipart uploads and their
// distinct limits will be configured when upload authorization is implemented.
func NewHandler(logger *slog.Logger) http.Handler {
	return newHandler(logger, &Resolver{})
}

func newHandler(logger *slog.Logger, resolvers ResolverRoot) http.Handler {
	srv := handler.New(NewExecutableSchema(Config{Resolvers: resolvers}))
	srv.SetErrorPresenter(errorPresenter(logger))
	srv.AddTransport(transport.POST{})
	srv.SetParserTokenLimit(4096)
	srv.Use(extension.FixedComplexityLimit(100))
	srv.SetRecoverFunc(func(context.Context, any) error {
		logger.Error("GraphQL resolver panic")
		return errors.New("internal server error")
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			reject(w, http.StatusMethodNotAllowed, codeMethodNotAllowed, "use POST")
			return
		}
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			reject(w, http.StatusUnsupportedMediaType, codeUnsupportedMedia, "use application/json")
			return
		}
		// Read at most the budget even for chunked/unknown-length requests. Buffering
		// guarantees a deterministic 413 before gqlgen starts decoding the payload.
		body := http.MaxBytesReader(w, r.Body, MaxRequestBytes)
		defer body.Close()
		payload, err := io.ReadAll(body)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				reject(w, http.StatusRequestEntityTooLarge, codeRequestTooLarge, "request too large")
			} else {
				reject(w, http.StatusBadRequest, codeInvalidInput, "invalid request body")
			}
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		r = r.Clone(ctx)
		r.Body = io.NopCloser(bytes.NewReader(payload))
		srv.ServeHTTP(w, r)
	})
}

// Messages are internal constants, never interpolated client or database errors.
func reject(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Fixed string values cannot fail JSON encoding. A write failure means the
	// client disconnected; another response cannot repair that connection.
	_ = json.NewEncoder(w).Encode(graphql.Response{Errors: gqlerror.List{
		{Message: message, Extensions: map[string]any{"code": code}},
	}})
}
