package upload

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLocalStoreImmutablePromotion(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := NewLocalStore(dir, runtime.GOOS != "linux")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	stage, err := Stage(ctx, t.TempDir(), strings.NewReader("durable bytes"), Metadata{Name: "file.txt"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	prepared, err := store.Prepare(ctx, stage)
	if err != nil {
		t.Fatal(err)
	}
	key := "blob-" + strings.Repeat("a", 64)
	if err = prepared.Promote(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err = prepared.Close(); err != nil {
		t.Fatal(err)
	}
	if err = prepared.Close(); err != nil {
		t.Fatal(err)
	}
	if err = store.Verify(ctx, key, 13); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(dir, key))
	if err != nil || string(content) != "durable bytes" {
		t.Fatal("published bytes changed")
	}
	second, err := store.Prepare(ctx, stage)
	if err != nil {
		t.Fatal(err)
	}
	if err = second.Promote(ctx, key); !errors.Is(err, ErrStorage) {
		t.Fatal("existing generation overwritten")
	}
	if err = second.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatal("prepared files leaked")
	}
	if err = store.Verify(ctx, key, 14); !errors.Is(err, ErrStorage) {
		t.Fatal("size mismatch accepted")
	}
	if err = store.Verify(ctx, "../escape", 13); !errors.Is(err, ErrStorage) {
		t.Fatal("unsafe key accepted")
	}
	if err = store.Verify(ctx, "blob-"+strings.Repeat("b", 64), 13); !errors.Is(err, ErrObjectMissing) {
		t.Fatal("missing object not distinguished")
	}
}

func TestLocalStoreInterruptedAndCorruptSource(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := NewLocalStore(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	staged, err := Stage(ctx, t.TempDir(), strings.NewReader("original"), Metadata{Name: "x"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer staged.Close()
	// A modified staging file must never become a published generation.
	if _, err = staged.file.WriteAt([]byte("modified"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Prepare(ctx, staged); !errors.Is(err, ErrStorage) {
		t.Fatal("corrupt source accepted")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = store.Prepare(cancelled, staged); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed preparation leaked files")
	}
}

func TestLocalStoreCancelledPromotion(t *testing.T) {
	store, err := NewLocalStore(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	staged, err := Stage(context.Background(), t.TempDir(), strings.NewReader(""), Metadata{Name: "empty"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer staged.Close()
	prepared, err := store.Prepare(context.Background(), staged)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	key := "blob-" + strings.Repeat("c", 64)
	if err = prepared.Promote(ctx, key); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled promotion succeeded")
	}
	if err = store.Verify(context.Background(), key, 0); !errors.Is(err, ErrObjectMissing) {
		t.Fatal("cancelled object exists")
	}
}
