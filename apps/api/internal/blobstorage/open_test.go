package blobstorage

import (
	"context"
	"testing"

	"full-stack-file-vault.local/api/internal/upload"
)

func TestProductionDefaultRemainsLocal(t *testing.T) {
	for _, backend := range []string{"", "local"} {
		t.Run(backend, func(t *testing.T) {
			t.Setenv("BLOB_STORAGE_BACKEND", backend)
			store, err := Open(context.Background(), "not a database URL", t.TempDir(), true)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if _, ok := store.(*upload.LocalStore); !ok {
				t.Fatal("default backend changed")
			}
		})
	}
}
func TestUnknownBackendFailsClosed(t *testing.T) {
	t.Setenv("BLOB_STORAGE_BACKEND", "postgres")
	if _, err := Open(context.Background(), "", t.TempDir(), true); err == nil {
		t.Fatal("unknown backend silently fell back to local storage")
	}
}
