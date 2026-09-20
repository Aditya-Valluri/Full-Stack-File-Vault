package upload

import (
	"context"
	"errors"
	"io"
	"os"
)

// Open returns a verified handle, never a physical path. An existing open handle
// may finish streaming after logical deletion; new opens require fresh authorization.
func (s *LocalStore) Open(ctx context.Context, key string, size int64) (io.ReadSeekCloser, error) {
	if err := s.Verify(ctx, key, size); err != nil {
		return nil, err
	}
	f, err := s.root.Open(key)
	if err != nil {
		return nil, ErrStorage
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != size {
		_ = f.Close()
		return nil, ErrStorage
	}
	return f, nil
}

// Remove deletes only one exact generation. Its caller must first durably fence
// publication and detach any blob reference. No directory traversal or sweep exists.
func (s *LocalStore) Remove(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validObjectKey(key) {
		return ErrStorage
	}
	info, err := s.root.Lstat(key)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrStorage
	}
	if err == nil {
		if !info.Mode().IsRegular() {
			return ErrStorage
		}
		if err = s.root.Remove(key); err != nil && !errors.Is(err, os.ErrNotExist) {
			return ErrStorage
		}
	}
	return s.syncDirectory()
}
