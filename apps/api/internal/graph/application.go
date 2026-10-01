package graph

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"full-stack-file-vault.local/api/internal/admin"
	"full-stack-file-vault.local/api/internal/auth"
	"full-stack-file-vault.local/api/internal/files"
	"full-stack-file-vault.local/api/internal/graph/model"
	"full-stack-file-vault.local/api/internal/sharing"
	"full-stack-file-vault.local/api/internal/upload"
	"github.com/99designs/gqlgen/graphql"
)

// Services groups optional domain services for focused transport fixtures.
// Production supplies both services.
type Services struct {
	Sharing        *sharing.Store
	Administration *admin.Store
}

// NewApplicationHandler composes browser checks, shared admission, and bounded
// GraphQL transports in that order. No REST upload route exists.
func NewApplicationHandler(logger *slog.Logger, browser *auth.BrowserSecurity, begin auth.BootstrapFunc, login auth.LoginFunc, logout auth.LogoutFunc, publisher *upload.Publisher, reader *files.Store, limiter *auth.UserLimiter, config MultipartConfig, services ...Services) (http.Handler, error) {
	if logger == nil || browser == nil || begin == nil || login == nil || logout == nil || publisher == nil || reader == nil || limiter == nil {
		return nil, errors.New("application dependencies required")
	}
	transport, err := NewMultipartTransport(config, parsedSchema, logger)
	if err != nil {
		return nil, err
	}
	var domains Services
	if len(services) > 1 {
		return nil, errors.New("at most one domain service set is supported")
	}
	if len(services) == 1 {
		domains = services[0]
	}
	handler := newHandlerWithUploads(logger, &Resolver{Publisher: publisher, FilesStore: reader, Sharing: domains.Sharing, Administration: domains.Administration}, transport)
	// Authenticated CSRF recovery uses the same user budget; otherwise the bootstrap
	// exception would provide a per-user admission bypass.
	limitedBegin := func(ctx context.Context, token string) (string, auth.Session, bool, error) {
		raw, state, created, err := begin(ctx, token)
		if err == nil && state.UserID != "" {
			err = limiter.Allow(ctx, state.UserID)
		}
		return raw, state, created, err
	}
	return browser.WrapAuthentication(limiter.Wrap(handler), validateBootstrapRequest, limitedBegin, login, logout), nil
}

func (r *Resolver) publish(ctx context.Context, inputs []*graphql.Upload, retryKey *string, tags []string) ([]*model.VaultFile, error) {
	if _, err := auth.RequireUser(ctx); err != nil {
		return nil, err
	}
	if r.Publisher == nil {
		return nil, upload.ErrPublication
	}
	staged := make([]*upload.Staged, len(inputs))
	for i, input := range inputs {
		if input == nil {
			return nil, upload.ErrInvalidInput
		}
		file, ok := input.File.(*upload.Staged)
		if !ok || file == nil {
			return nil, upload.ErrInvalidInput
		}
		staged[i] = file
	}
	var keys []string
	if retryKey != nil {
		keys = append(keys, *retryKey)
	}
	files, err := r.Publisher.PublishWithTags(ctx, staged, tags, keys...)
	if err != nil {
		return nil, err
	}
	result := make([]*model.VaultFile, 0, len(files))
	for _, file := range files {
		result = append(result, &model.VaultFile{ID: file.ID, Name: file.Name, SizeBytes: strconv.FormatInt(file.SizeBytes, 10), DetectedMime: file.DetectedMIME, CreatedAt: file.CreatedAt, Tags: append([]string{}, file.Tags...)})
	}
	return result, nil
}

func fileModel(file files.File) *model.VaultFile {
	return &model.VaultFile{ID: file.ID, Name: file.Name, SizeBytes: strconv.FormatInt(file.SizeBytes, 10), DetectedMime: file.DetectedMIME, CreatedAt: file.CreatedAt, Tags: file.Tags, FolderID: file.FolderID}
}
