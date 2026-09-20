package upload

import (
	"context"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"
)

func TestLocalObjectReadAndRemove(t *testing.T) {
	ctx := context.Background()
	storage, err := NewLocalStore(t.TempDir(), runtime.GOOS != "linux")
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	source, err := Stage(ctx, t.TempDir(), strings.NewReader("range content"), Metadata{Name: "sample"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	prepared, err := storage.Prepare(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	key := "blob-" + strings.Repeat("e", 64)
	if err := prepared.Promote(ctx, key); err != nil {
		t.Fatal(err)
	}
	handle, err := storage.Open(ctx, key, 13)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handle.Seek(6, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "linux" {
		if err := storage.Remove(ctx, key); err != nil {
			t.Fatal(err)
		}
	}
	body, err := io.ReadAll(handle)
	if err != nil || string(body) != "content" {
		t.Fatal("stream changed", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}
	if err := storage.Remove(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err := storage.Remove(ctx, key); err != nil {
		t.Fatal("retry not idempotent", err)
	}
	if _, err := storage.Open(ctx, key, 13); !errors.Is(err, ErrObjectMissing) {
		t.Fatal("deleted content opened")
	}
	if err := storage.Remove(ctx, "../outside"); !errors.Is(err, ErrStorage) {
		t.Fatal("unsafe key accepted")
	}
}
