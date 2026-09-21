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

	"full-stack-file-vault.local/api/internal/graph/model"
	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

const MaxRequestBytes int64 = 64 << 10

// NewHandler supplies the JSON-only foundation for focused transport tests.
func NewHandler(logger *slog.Logger) http.Handler {
	return newHandler(logger, &Resolver{})
}

func newHandler(logger *slog.Logger, resolvers ResolverRoot) http.Handler {
	return newHandlerWithUploads(logger, resolvers, nil)
}
func newHandlerWithUploads(logger *slog.Logger, resolvers ResolverRoot, multipart *MultipartTransport) http.Handler {
	schemaConfig := Config{Resolvers: resolvers}
	// Charge list selections by their bounded cardinality, including aliased queries.
	schemaConfig.Complexity.Query.Files = func(child int, first int, _ *string, _ *model.FileFilter) int {
		return 1 + max(1, min(first, 50))*child
	}
	schemaConfig.Complexity.Query.FileShares = func(child int, _ string) int { return 1 + 20*child }
	schemaConfig.Complexity.Query.AdminUsers = func(child, first int, _ *string) int { return 1 + max(1, min(first, 50))*child }
	schemaConfig.Complexity.Query.AdminFiles = func(child, first int, _, _ *string) int { return 1 + max(1, min(first, 50))*child }
	schemaConfig.Complexity.Query.AdminAudit = func(child, first int, _ *string) int { return 1 + max(1, min(first, 50))*child }
	srv := handler.New(NewExecutableSchema(schemaConfig))
	if multipart != nil {
		srv.AddTransport(multipart)
	}
	srv.SetErrorPresenter(errorPresenter(logger))
	srv.AddTransport(transport.POST{})
	srv.SetParserTokenLimit(4096)
	srv.Use(extension.FixedComplexityLimit(500))
	srv.AroundOperations(func(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
		op := graphql.GetOperationContext(ctx)
		if op.Operation.Operation == ast.Mutation && !singleMutationRoot(op.Doc, op.Operation) {
			return func(context.Context) *graphql.Response {
				return &graphql.Response{Errors: gqlerror.List{&gqlerror.Error{Message: "use one mutation root without directives", Extensions: map[string]any{"code": codeInvalidInput}}}}
			}
		}
		return next(ctx)
	})
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
		if err == nil && mediaType == "multipart/form-data" && multipart != nil {
			srv.ServeHTTP(w, r)
			return
		}
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
