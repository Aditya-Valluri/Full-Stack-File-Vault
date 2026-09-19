package graph

import (
	"file-vault.local/api/internal/auth"
	"context"
	"errors"
	"log/slog"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

const (
	codeInternal         = "INTERNAL_ERROR"
	codeInvalidInput     = "INVALID_INPUT"
	codeMethodNotAllowed = "METHOD_NOT_ALLOWED"
	codeUnsupportedMedia = "UNSUPPORTED_MEDIA_TYPE"
	codeRequestTooLarge  = "REQUEST_TOO_LARGE"
)

// errorPresenter trusts only known pre-execution protocol errors. Resolver errors
// are private by default, even if wrapped as gqlerror with a protocol-looking code.
// Introduce explicitly typed public domain errors alongside future business rules.
func errorPresenter(logger *slog.Logger) graphql.ErrorPresenterFunc {
	return func(ctx context.Context, err error) *gqlerror.Error {
		if errors.Is(err, auth.ErrBootstrapLimited) {
			return &gqlerror.Error{Message: "session creation temporarily limited", Path: graphql.GetPath(ctx), Extensions: map[string]any{"code": "RATE_LIMITED"}}
		}
		if errors.Is(err, auth.ErrBootstrapForbidden) {
			return &gqlerror.Error{Message: "session bootstrap not authorized", Path: graphql.GetPath(ctx), Extensions: map[string]any{"code": "FORBIDDEN"}}
		}
		if graphql.GetFieldContext(ctx) == nil {
			var protocol *gqlerror.Error
			if errors.As(err, &protocol) {
				code, _ := protocol.Extensions["code"].(string)
				switch code {
				case "GRAPHQL_PARSE_FAILED", "GRAPHQL_VALIDATION_FAILED", "COMPLEXITY_LIMIT_EXCEEDED":
					return &gqlerror.Error{Message: protocol.Message, Locations: protocol.Locations,
						Extensions: map[string]any{"code": code}}
				}
			}
			// gqlgen's malformed-JSON error can contain the entire request body. Do not
			// echo it or log it, including when authentication fields are added later.
			return &gqlerror.Error{Message: "invalid GraphQL request", Extensions: map[string]any{"code": codeInvalidInput}}
		}
		logger.Error("GraphQL execution failed", "code", codeInternal)
		return &gqlerror.Error{Message: "internal server error", Path: graphql.GetPath(ctx),
			Extensions: map[string]any{"code": codeInternal}}
	}
}
