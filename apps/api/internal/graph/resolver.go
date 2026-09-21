package graph

import (
	"full-stack-file-vault.local/api/internal/admin"
	"full-stack-file-vault.local/api/internal/files"
	"full-stack-file-vault.local/api/internal/sharing"
	"full-stack-file-vault.local/api/internal/upload"
)

// Resolver holds focused services; ownership and transaction rules stay in them.
type Resolver struct {
	Administration *admin.Store
	Sharing        *sharing.Store
	Publisher      *upload.Publisher
	FilesStore     *files.Store
}
