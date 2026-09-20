package graph

import (
	"context"
	"file-vault.local/api/internal/auth"
	"file-vault.local/api/internal/files"
	"file-vault.local/api/internal/graph/model"
	"file-vault.local/api/internal/sharing"
)

func (r *Resolver) requireSharing(ctx context.Context, owner bool) (*sharing.Store, error) {
	if owner {
		if _, err := auth.RequireUser(ctx); err != nil {
			return nil, err
		}
	} else {
		if _, _, err := auth.BrowserBinding(ctx); err != nil {
			return nil, err
		}
	}
	if r.Sharing == nil {
		return nil, files.ErrUnavailable
	}
	return r.Sharing, nil
}
func shareModel(sh sharing.Share) *model.FileShare {
	return &model.FileShare{ID: sh.ID, RecipientID: sh.RecipientID, Permission: model.SharePermission(sh.Permission), CreatedAt: sh.CreatedAt, ExpiresAt: sh.ExpiresAt}
}
