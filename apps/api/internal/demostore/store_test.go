package demostore

import (
	"context"
	"errors"
	"strings"
	"testing"

	"full-stack-file-vault.local/api/internal/upload"
)

func TestPreparedMemoryIsBoundedAndReleased(t *testing.T) {
	ctx := context.Background()
	staged, err := upload.Stage(ctx, t.TempDir(), strings.NewReader(strings.Repeat("x", 10000000)), upload.Metadata{Name: "limit.txt"}, 10000000)
	if err != nil {
		t.Fatal(err)
	}
	defer staged.Close()
	store := &BlobStore{}
	first, err := store.Prepare(ctx, staged)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := store.Prepare(ctx, staged)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err = store.Prepare(ctx, staged); !errors.Is(err, upload.ErrStorage) {
		t.Fatal("unbounded prepared memory", err)
	}
	first.Close()
	third, err := store.Prepare(ctx, staged)
	if err != nil {
		t.Fatal("capacity not released", err)
	}
	defer third.Close()
	second.Close()
	third.Close()
	third.Close()
	if store.preparedBytes != 0 {
		t.Fatal("prepared memory accounting drift")
	}
}
