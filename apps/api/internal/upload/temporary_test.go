package upload

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestTemporaryCleanerPreservesActiveAndFinalFiles(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("file leases unavailable")
	}
	dir := t.TempDir()
	staged, err := Stage(context.Background(), dir, strings.NewReader("safe"), Metadata{Name: "safe.txt"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer staged.Close()
	store, err := NewLocalStore(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	prepared, err := store.Prepare(context.Background(), staged)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	key := "blob-" + strings.Repeat("a", 64)
	if err = prepared.Promote(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	cleaner, err := NewTemporaryCleaner(dir, dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer cleaner.Close()
	cleaner.grace = 0
	if count, err := cleaner.RunOnce(context.Background()); err != nil || count != 0 {
		t.Fatalf("active files removed: %d %v", count, err)
	}
	// Simulate the kernel releasing handles after a crash, while directory entries remain.
	if err = staged.file.Close(); err != nil {
		t.Fatal(err)
	}
	staged.file = nil
	candidate := prepared.(*localPrepared)
	if err = candidate.file.Close(); err != nil {
		t.Fatal(err)
	}
	candidate.file = nil
	removed := 0
	for i := 0; i < 4; i++ {
		n, err := cleaner.RunOnce(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		removed += n
	}
	if removed != 2 {
		t.Fatalf("abandoned files removed=%d", removed)
	}
	if content, err := os.ReadFile(filepath.Join(dir, key)); err != nil || string(content) != "safe" {
		t.Fatal("final generation damaged", err)
	}
}

func TestTemporaryCleanerCursorAndAge(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("file leases unavailable")
	}
	dir := t.TempDir()
	old := time.Now().Add(-2 * time.Hour)
	for i := 0; i < 25; i++ {
		key, err := randomStorageKey(".candidate-")
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, key)
		if err = os.WriteFile(path, []byte("old"), 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	fresh := "upload-" + strings.Repeat("b", 64) + ".part"
	for _, name := range []string{fresh, ".candidate-invalid", "other.txt", "blob-" + strings.Repeat("c", 64)} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cleaner, err := NewTemporaryCleaner(dir, dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer cleaner.Close()
	removed := 0
	for i := 0; i < 10; i++ {
		n, err := cleaner.RunOnce(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		removed += n
	}
	if removed != 25 {
		t.Fatalf("cursor missed abandoned files: %d", removed)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 4 {
		t.Fatal("unexpected files removed", err)
	}
}

func TestTemporaryCrashHelper(t *testing.T) {
	dir := os.Getenv("VAULT_TEMP_CRASH_HELPER")
	if dir == "" {
		return
	}
	file, err := Stage(context.Background(), dir, strings.NewReader("crash"), Metadata{Name: "crash.txt"}, 100)
	if err != nil {
		os.Exit(2)
	}
	store, err := NewLocalStore(dir, true)
	if err != nil {
		os.Exit(3)
	}
	if _, err = store.Prepare(context.Background(), file); err != nil {
		os.Exit(4)
	}
	os.Exit(0) // Deliberately bypass all cleanup to model abrupt process termination.
}

func TestTemporaryCleanerAfterProcessExit(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("file leases unavailable")
	}
	dir := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestTemporaryCrashHelper$")
	command.Env = append(os.Environ(), "VAULT_TEMP_CRASH_HELPER="+dir)
	if err = command.Run(); err != nil {
		t.Fatal(err)
	}
	cleaner, err := NewTemporaryCleaner(dir, dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer cleaner.Close()
	cleaner.grace = 0
	if n, err := cleaner.RunOnce(ctx); err != nil || n != 2 {
		t.Fatalf("crash recovery removed=%d err=%v", n, err)
	}
}
