package graph

import (
	"file-vault.local/api/internal/admin"
	"file-vault.local/api/internal/files"
	"file-vault.local/api/internal/sharing"
	"file-vault.local/api/internal/upload"
)

// Resolver holds focused services; ownership and transaction rules stay in them.
type Resolver struct {
	Administration *admin.Store
	Sharing        *sharing.Store
	Publisher      *upload.Publisher
	FilesStore     *files.Store
}
