package upload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

func assertEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("staging leftovers: count=%d err=%v", len(entries), err)
	}
}

func TestStageStreamsHashesAndRewinds(t *testing.T) {
	dir := t.TempDir()
	content := bytes.Repeat([]byte("bounded stream\n"), 10000)
	source := bytes.NewReader(content)
	largestRead := 0
	tracked := readerFunc(func(p []byte) (int, error) {
		if len(p) > largestRead {
			largestRead = len(p)
		}
		return source.Read(p)
	})
	file, err := Stage(context.Background(), dir, tracked, Metadata{Name: "report.pdf", DeclaredMIME: "Application/PDF"}, int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info := file.Info()
	if info.SHA256 != sha256.Sum256(content) || info.SizeBytes != int64(len(content)) {
		t.Fatal("incorrect digest/size")
	}
	if info.Name != "report.pdf" || info.DeclaredMIME != "application/pdf" || info.DetectedMIME != "text/plain; charset=utf-8" {
		t.Fatalf("client claim trusted or metadata changed: %+v", info.Metadata)
	}
	if largestRead > 32*1024 {
		t.Fatal("read buffer unbounded")
	}
	got, err := io.ReadAll(file)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatal("staged bytes differ")
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	prefix := make([]byte, 7)
	if _, err = io.ReadFull(file, prefix); err != nil || string(prefix) != "bounded" {
		t.Fatal("seek failed")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatal("incorrect temporary files")
	}
	if entries[0].Name() == info.Name || !strings.HasPrefix(entries[0].Name(), "upload-") {
		t.Fatal("client filename used as path")
	}
	stat, err := entries[0].Info()
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && stat.Mode().Perm() != 0600 {
		t.Fatal("unsafe file mode")
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal("cleanup not idempotent")
	}
	if _, err = file.Read(prefix); !errors.Is(err, os.ErrClosed) {
		t.Fatal("read after cleanup accepted")
	}
	assertEmpty(t, dir)
}

func TestStageSizeAndEmptyFiles(t *testing.T) {
	for _, tc := range []struct {
		name        string
		size, limit int
		tooLarge    bool
	}{{"empty", 0, 4, false}, {"below", 3, 4, false}, {"exact", 4, 4, false}, {"over", 5, 4, true}, {"large", 100000, 4, true}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			source := strings.NewReader(strings.Repeat("a", tc.size))
			file, err := Stage(context.Background(), dir, source, Metadata{Name: "file"}, int64(tc.limit))
			if tc.tooLarge {
				if !errors.Is(err, ErrTooLarge) || file != nil {
					t.Fatal("oversize accepted")
				}
				if tc.size-source.Len() != tc.limit+1 {
					t.Fatal("consumed beyond size probe")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if file.Info().SizeBytes != int64(tc.size) || file.Info().SHA256 != sha256.Sum256([]byte(strings.Repeat("a", tc.size))) {
					t.Fatal("wrong empty/boundary digest")
				}
				if err = file.Close(); err != nil {
					t.Fatal(err)
				}
			}
			assertEmpty(t, dir)
		})
	}
}

func TestStageFailureCleanup(t *testing.T) {
	for _, kind := range []string{"source error", "data plus error", "stalled", "cancel before", "cancel during", "bad reader"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			want := ErrRead
			reader := readerFunc(func(p []byte) (int, error) {
				calls++
				switch kind {
				case "source error":
					return 0, errors.New("private source path")
				case "data plus error":
					copy(p, "abc")
					return 3, errors.New("private source path")
				case "cancel during":
					cancel()
					copy(p, "abc")
					return 3, nil
				case "bad reader":
					return len(p) + 1, nil
				default:
					return 0, nil
				}
			})
			if kind == "cancel before" {
				cancel()
				want = context.Canceled
			}
			if kind == "cancel during" {
				want = context.Canceled
			}
			staged, err := Stage(ctx, dir, reader, Metadata{Name: "test"}, 100)
			if !errors.Is(err, want) || staged != nil {
				t.Fatalf("error=%v", err)
			}
			if strings.Contains(err.Error(), "private") {
				t.Fatal("source error leaked")
			}
			if kind == "cancel before" && calls != 0 {
				t.Fatal("read after cancellation")
			}
			if kind == "stalled" && calls != 100 {
				t.Fatal("unbounded empty reads")
			}
			assertEmpty(t, dir)
		})
	}
}

func TestStageMetadataValidationBeforeIO(t *testing.T) {
	for _, metadata := range []Metadata{
		{Name: ""}, {Name: " "}, {Name: ".."}, {Name: "../escape"}, {Name: `C:\escape`}, {Name: "bad\x00name"}, {Name: string([]byte{0xff})}, {Name: strings.Repeat("x", 256)},
		{Name: "ok", DeclaredMIME: "not-a-mime"}, {Name: "ok", DeclaredMIME: "image/*"}, {Name: "ok", DeclaredMIME: strings.Repeat("x", 257)},
	} {
		dir := t.TempDir()
		calls := 0
		file, err := Stage(context.Background(), dir, readerFunc(func([]byte) (int, error) { calls++; return 0, io.EOF }), metadata, 100)
		if !errors.Is(err, ErrInvalidInput) || file != nil || calls != 0 {
			t.Fatal("invalid metadata reached IO")
		}
		assertEmpty(t, dir)
	}
	dir := t.TempDir()
	if _, err := Stage(context.Background(), filepath.Join(dir, "missing"), strings.NewReader("x"), Metadata{Name: "ok"}, 10); !errors.Is(err, ErrStorage) {
		t.Fatal("missing directory accepted")
	}
	if _, err := Stage(context.Background(), dir, strings.NewReader("x"), Metadata{Name: "ok"}, 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("zero limit accepted")
	}
	assertEmpty(t, dir)
}

func TestStageConcurrentIdenticalContent(t *testing.T) {
	dir := t.TempDir()
	const count = 12
	results := make(chan *Staged, count)
	failures := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := Stage(context.Background(), dir, strings.NewReader("same bytes"), Metadata{Name: "same.txt"}, 100)
			if err != nil {
				failures <- err
				return
			}
			results <- s
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != count {
		t.Fatal("temporary names collided")
	}
	for s := range results {
		if s.Info().SHA256 != sha256.Sum256([]byte("same bytes")) {
			t.Error("digest mismatch")
		}
		if err = s.Close(); err != nil {
			t.Error(err)
		}
	}
	assertEmpty(t, dir)
}

func TestStageAcceptsDataWithEOF(t *testing.T) {
	dir := t.TempDir()
	file, err := Stage(context.Background(), dir, readerFunc(func(p []byte) (int, error) { copy(p, "abc"); return 3, io.EOF }), Metadata{Name: "file"}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if file.Info().SizeBytes != 3 {
		t.Fatal("EOF discarded data")
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	assertEmpty(t, dir)
}

func TestStagePanicStillCleansTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected source panic")
			}
		}()
		_, _ = Stage(context.Background(), dir, readerFunc(func([]byte) (int, error) { panic("source failed") }), Metadata{Name: "file"}, 10)
	}()
	assertEmpty(t, dir)
}

func TestCloseReportsAndRetriesRemovalFailure(t *testing.T) {
	dir := t.TempDir()
	file, err := Stage(context.Background(), dir, strings.NewReader("x"), Metadata{Name: "file"}, 4)
	if err != nil {
		t.Fatal(err)
	}
	// Replace the temporary entry with a nonempty directory to model filesystem
	// interference. Cleanup must report failure, never recursively delete it.
	if err = file.file.Close(); err != nil {
		t.Fatal(err)
	}
	file.file = nil
	if err = file.root.Remove(file.name); err != nil {
		t.Fatal(err)
	}
	if err = file.root.Mkdir(file.name, 0700); err != nil {
		t.Fatal(err)
	}
	child := file.name + "/keep"
	if err = file.root.WriteFile(child, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); !errors.Is(err, ErrCleanup) {
		t.Fatal("failed cleanup hidden")
	}
	if got, err := file.root.ReadFile(child); err != nil || string(got) != "preserve" {
		t.Fatal("recursive deletion occurred")
	}
	if err = file.root.Remove(child); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	assertEmpty(t, dir)
}
