package upload

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
)

var ErrObjectMissing = errors.New("stored content unavailable")

// LocalStore uses one private filesystem directory for prepared and final objects.
// Development explicitly permits weaker directory durability on Windows; production
// mode requires Linux and a filesystem supporting hard links and directory fsync.
// Close only after all publications and prepared handles have finished.
type LocalStore struct {
	root        *os.Root
	development bool
}

func NewLocalStore(directory string, development bool) (*LocalStore, error) {
	if !development && runtime.GOOS != "linux" {
		return nil, errors.New("durable local publication requires Linux")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, ErrStorage
	}
	store := &LocalStore{root: root, development: development}
	if err = store.syncDirectory(); err != nil {
		_ = root.Close()
		return nil, err
	}
	return store, nil
}

func (s *LocalStore) Close() error { return s.root.Close() }

func (s *LocalStore) syncDirectory() error {
	if s.development {
		return nil
	}
	dir, err := s.root.Open(".")
	if err != nil {
		return ErrStorage
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil || closeErr != nil {
		return ErrStorage
	}
	return nil
}

func randomStorageKey(prefix string) (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", ErrStorage
	}
	return prefix + hex.EncodeToString(bytes[:]), nil
}

func validObjectKey(key string) bool {
	if !strings.HasPrefix(key, "blob-") || len(key) != 69 {
		return false
	}
	for _, c := range key[5:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func (s *LocalStore) Prepare(ctx context.Context, source *Staged) (result PreparedObject, err error) {
	if source == nil {
		return nil, ErrInvalidInput
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	name, err := randomStorageKey(".candidate-")
	if err != nil {
		return nil, err
	}
	f, err := s.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, ErrStorage
	}
	closed := false
	defer func() {
		if !closed {
			_ = f.Close()
		}
		if result == nil {
			if e := s.root.Remove(name); e != nil && !errors.Is(e, os.ErrNotExist) {
				err = errors.Join(err, ErrCleanup)
			}
		}
	}()
	locked, lockErr := lockTemporary(f)
	if lockErr != nil || !locked {
		return nil, ErrStorage
	}
	if _, err = source.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	info := source.Info()
	hash := sha256.New()
	buffer := make([]byte, 32*1024)
	var size int64
	for {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		n, e := source.Read(buffer)
		if n > 0 {
			if int64(n) > info.SizeBytes-size {
				return nil, ErrStorage
			}
			written, writeErr := f.Write(buffer[:n])
			if writeErr != nil || written != n {
				return nil, ErrStorage
			}
			_, _ = hash.Write(buffer[:n])
			size += int64(n)
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, ErrStorage
		}
	}
	var digest [32]byte
	copy(digest[:], hash.Sum(nil))
	if size != info.SizeBytes || digest != info.SHA256 {
		return nil, ErrStorage
	}
	if err = f.Sync(); err != nil {
		return nil, ErrStorage
	}
	// Transfer the open handle and its lease until publication/cleanup finishes.
	closed = true
	return &localPrepared{store: s, name: name, file: f}, nil
}

// Verify checks existence/type/size, not whole-content bit rot. Generations are
// immutable and verified by hashing before promotion; the directory must be private.
func (s *LocalStore) Verify(ctx context.Context, key string, size int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validObjectKey(key) {
		return ErrStorage
	}
	info, err := s.root.Lstat(key)
	if errors.Is(err, os.ErrNotExist) {
		return ErrObjectMissing
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() != size {
		return ErrStorage
	}
	return nil
}

type localPrepared struct {
	mu               sync.Mutex
	store            *LocalStore
	name             string
	file             *os.File
	closed, promoted bool
}

func (p *localPrepared) Promote(ctx context.Context, key string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.promoted || !validObjectKey(key) {
		return ErrStorage
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Link is atomic and fails if the destination exists; Rename could overwrite.
	// The temporary hard link remains until Close, so a failed DB commit never
	// triggers deletion of a possibly committed final generation.
	if err := p.store.root.Link(p.name, key); err != nil {
		return ErrStorage
	}
	p.promoted = true
	return p.store.syncDirectory()
}

func (p *localPrepared) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	if err := p.store.root.Remove(p.name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrCleanup
	}
	p.closed = true
	if p.file != nil {
		return p.file.Close()
	}
	return nil
}
