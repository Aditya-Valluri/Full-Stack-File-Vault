package graph

import (
	"context"
	"errors"
	"full-stack-file-vault.local/api/internal/admin"
	"full-stack-file-vault.local/api/internal/auth"
	"full-stack-file-vault.local/api/internal/files"
	"full-stack-file-vault.local/api/internal/sharing"
	"full-stack-file-vault.local/api/internal/telemetry"
	"full-stack-file-vault.local/api/internal/upload"
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
	present := domainErrorPresenter(logger)
	return func(ctx context.Context, err error) *gqlerror.Error {
		result := present(ctx, err)
		code, _ := result.Extensions["code"].(string)
		telemetry.Default.GraphQLError(code)
		return result
	}
}
func domainErrorPresenter(logger *slog.Logger) graphql.ErrorPresenterFunc {
	return func(ctx context.Context, err error) *gqlerror.Error {
		if errors.Is(err, files.ErrPreviewUnsupported) {
			return &gqlerror.Error{Message: "preview unavailable for this content type", Path: graphql.GetPath(ctx), Extensions: map[string]any{"code": "UNSUPPORTED_MEDIA_TYPE"}}
		}
		if errors.Is(err, admin.ErrForbidden) {
			return &gqlerror.Error{Message: "administrator access required", Path: graphql.GetPath(ctx), Extensions: map[string]any{"code": "FORBIDDEN"}}
		}
		if errors.Is(err, upload.ErrRetryConflict) {
			return &gqlerror.Error{Message: "upload retry content differs", Path: graphql.GetPath(ctx), Extensions: map[string]any{"code": "UPLOAD_RETRY_CONFLICT"}}
		}
		if errors.Is(err, admin.ErrConflict) {
			return &gqlerror.Error{Message: "change conflicts with current user state", Path: graphql.GetPath(ctx), Extensions: map[string]any{"code": "CONFLICT"}}
		}
		if errors.Is(err, sharing.ErrLimit) {
			return &gqlerror.Error{Message: "sharing capacity reached", Path: graphql.GetPath(ctx), Extensions: map[string]any{"code": "RATE_LIMITED"}}
		}
		if errors.Is(err, files.ErrAccessLimit) {
			return &gqlerror.Error{Message: "too many active file access grants", Path: graphql.GetPath(ctx), Extensions: map[string]any{"code": "RATE_LIMITED"}}
		}
		if errors.Is(err, files.ErrNotFound) {
			return &gqlerror.Error{Message: "file not found", Path: graphql.GetPath(ctx), Extensions: map[string]any{"code": "NOT_FOUND"}}
		}
		if errors.Is(err, files.ErrInvalidInput) {
			return &gqlerror.Error{Message: "invalid file query", Path: graphql.GetPath(ctx), Extensions: map[string]any{"code": codeInvalidInput}}
		}
		var rateError *auth.RateLimitError
		if errors.As(err, &rateError) {
			return &gqlerror.Error{Message: "user request rate exceeded", Path: graphql.GetPath(ctx), Extensions: map[string]any{"code": "RATE_LIMITED"}}
		}
		if errors.Is(err, upload.ErrDemoCapacity) {
			return &gqlerror.Error{Message: "temporary demo storage capacity reached", Path: graphql.GetPath(ctx), Extensions: map[string]any{"code": "DEMO_CAPACITY_REACHED"}}
		}
		if errors.Is(err, upload.ErrQuotaExceeded) {
			return &gqlerror.Error{Message: "storage quota exceeded", Path: graphql.GetPath(ctx), Extensions: map[string]any{"code": "QUOTA_EXCEEDED"}}
		}
		if errors.Is(err, upload.ErrInvalidInput) {
			return &gqlerror.Error{Message: "invalid upload", Path: graphql.GetPath(ctx), Extensions: map[string]any{"code": codeInvalidInput}}
		}
		if errors.Is(err, auth.ErrUnauthenticated) {
			return &gqlerror.Error{Message: "authentication required", Path: graphql.GetPath(ctx), Extensions: map[string]any{"code": "UNAUTHENTICATED"}}
		}
		if errors.Is(err, auth.ErrLoginLimited) {
			return &gqlerror.Error{Message: "login temporarily limited", Path: graphql.GetPath(ctx), Extensions: map[string]any{"code": "RATE_LIMITED"}}
		}
		if errors.Is(err, auth.ErrLoginRejected) {
			return &gqlerror.Error{Message: "invalid login credentials", Path: graphql.GetPath(ctx), Extensions: map[string]any{"code": "UNAUTHENTICATED"}}
		}
		if errors.Is(err, auth.ErrLoginForbidden) {
			return &gqlerror.Error{Message: "login requires an anonymous session", Path: graphql.GetPath(ctx), Extensions: map[string]any{"code": "FORBIDDEN"}}
		}
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
					// Validation/coercion messages can quote passwords supplied as literals
					// or variables. Keep the code and locations, never the supplied value.
					return &gqlerror.Error{Message: "invalid GraphQL operation", Locations: protocol.Locations,
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
