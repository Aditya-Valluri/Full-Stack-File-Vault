// Package blobstorage selects byte storage explicitly; disk storage is the default.
package blobstorage

import (
	"context"
	"errors"
	"io"
	"os"

	"full-stack-file-vault.local/api/internal/demostore"
	"full-stack-file-vault.local/api/internal/upload"
)

// BlobStore composes the existing publication, content, and cleanup contracts.
// The production contracts themselves are unchanged.
type BlobStore interface {
	upload.PublicationStore
	Open(context.Context, string, int64) (io.ReadSeekCloser, error)
	Remove(context.Context, string) error
	Close() error
}

func Open(ctx context.Context, dsn, directory string, development bool) (BlobStore, error) {
	switch os.Getenv("BLOB_STORAGE_BACKEND") {
	case "", "local":
		return upload.NewLocalStore(directory, development)
	case "postgres-demo":
		return demostore.Open(ctx, dsn)
	default:
		return nil, errors.New("BLOB_STORAGE_BACKEND must be local or postgres-demo")
	}
}
