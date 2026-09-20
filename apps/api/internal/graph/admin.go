package graph

import (
	"context"
	"file-vault.local/api/internal/admin"
	"file-vault.local/api/internal/auth"
	"file-vault.local/api/internal/files"
	"file-vault.local/api/internal/graph/model"
)

func (r *Resolver) requireAdminService(ctx context.Context) (*admin.Store, error) {
	if _, err := auth.RequireUser(ctx); err != nil {
		return nil, err
	}
	if r.Administration == nil {
		return nil, files.ErrUnavailable
	}
	return r.Administration, nil
}
func adminUserModel(user admin.User) *model.AdminUser {
	return &model.AdminUser{ID: user.ID, LoginName: user.LoginName, Role: model.UserRole(user.Role), UsedBytes: user.UsedBytes, QuotaBytes: user.QuotaBytes, DisabledAt: user.DisabledAt, CreatedAt: user.CreatedAt}
}
