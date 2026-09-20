// Package upload provides bounded staging and transactional content publication.
// Stage itself does not authorize callers or publish logical files.
package upload

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"mime"
	"net/http"
	"os"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

var (
	ErrInvalidInput = errors.New("invalid upload metadata or limit")
	ErrTooLarge     = errors.New("upload exceeds file size limit")
	ErrStorage      = errors.New("upload staging storage unavailable")
	ErrRead         = errors.New("upload source failed")
	ErrCleanup      = errors.New("upload staging cleanup failed")
)

// Metadata contains untrusted display information, never filesystem paths.
type Metadata struct {
	Name         string
	DeclaredMIME string
}

// Info is internal upload-service data. SHA256 must not be exposed in user-facing
// responses or used as proof that the caller owns existing content.
type Info struct {
	Metadata
	SizeBytes    int64
	SHA256       [sha256.Size]byte
	DetectedMIME string
}

// Staged is a seekable, read-only view of temporary content. Close removes it.
// Read, Seek and Close serialize access, but callers must coordinate sequences of
// reads/seeks. No final blob durability or publication is implied by staging.
type Staged struct {
	mu      sync.Mutex
	root    *os.Root
	file    *os.File
	name    string
	info    Info
	removed bool
}

// Stage streams at most maxBytes to a randomly named, exclusively created file in
// an existing service-owned directory. It consumes at most maxBytes+1 source bytes
// to distinguish an exact-size upload from an oversized one. The caller owns src
// and must enforce authorization, request deadlines and aggregate disk admission.
// Cancellation is checked between reads; it cannot interrupt a blocked io.Reader.
// Always Close a successful result, including on later validation/DB failures.
func Stage(ctx context.Context, directory string, src io.Reader, metadata Metadata, maxBytes int64) (result *Staged, err error) {
	metadata, err = validateMetadata(metadata)
	if err != nil || src == nil || maxBytes < 1 || maxBytes == math.MaxInt64 {
		return nil, ErrInvalidInput
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, ErrStorage
	}
	var random [32]byte
	if _, err = rand.Read(random[:]); err != nil {
		_ = root.Close()
		return nil, ErrStorage
	}
	name := "upload-" + hex.EncodeToString(random[:]) + ".part"
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		_ = root.Close()
		return nil, ErrStorage
	}
	staged := &Staged{root: root, file: file, name: name, info: Info{Metadata: metadata}}
	defer func() {
		if result == nil {
			if cleanupErr := staged.Close(); cleanupErr != nil {
				// Failed Stage returns no handle. Release the root even if filesystem damage
				// prevented removal; report cleanup failure for operational recovery.
				_ = root.Close()
				err = errors.Join(err, ErrCleanup)
			}
		}
	}()
	locked, lockErr := lockTemporary(file)
	if lockErr != nil || !locked {
		return nil, ErrStorage
	}
	hash := sha256.New()
	var prefix [512]byte
	sniffed := 0
	buffer := make([]byte, 32*1024)
	emptyReads := 0
	for {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		remaining := maxBytes - staged.info.SizeBytes
		readSize := int64(len(buffer))
		if remaining < readSize {
			readSize = remaining + 1
		}
		n, readErr := src.Read(buffer[:int(readSize)])
		if n < 0 || n > int(readSize) {
			return nil, ErrRead
		}
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if int64(n) > remaining {
			return nil, ErrTooLarge
		}
		if n > 0 {
			emptyReads = 0
			if written, writeErr := file.Write(buffer[:n]); writeErr != nil || written != n {
				return nil, ErrStorage
			}
			_, _ = hash.Write(buffer[:n]) // hash.Hash.Write cannot return an error.
			sniffed += copy(prefix[sniffed:], buffer[:n])
			staged.info.SizeBytes += int64(n)
		} else if readErr == nil {
			emptyReads++
			if emptyReads >= 100 {
				return nil, ErrRead
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				return nil, ErrRead
			}
			break
		}
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return nil, ErrStorage
	}
	copy(staged.info.SHA256[:], hash.Sum(nil))
	staged.info.DetectedMIME = http.DetectContentType(prefix[:sniffed])
	return staged, nil
}

func validateMetadata(m Metadata) (Metadata, error) {
	if !utf8.ValidString(m.Name) || utf8.RuneCountInString(m.Name) > 255 || strings.TrimSpace(m.Name) == "" || m.Name == "." || m.Name == ".." || strings.ContainsAny(m.Name, "/\\") {
		return Metadata{}, ErrInvalidInput
	}
	for _, r := range m.Name {
		if unicode.IsControl(r) {
			return Metadata{}, ErrInvalidInput
		}
	}
	if len(m.DeclaredMIME) > 256 {
		return Metadata{}, ErrInvalidInput
	}
	if m.DeclaredMIME != "" {
		media, _, err := mime.ParseMediaType(m.DeclaredMIME)
		if err != nil || !strings.Contains(media, "/") || strings.Contains(media, "*") {
			return Metadata{}, ErrInvalidInput
		}
		m.DeclaredMIME = media
	}
	return m, nil
}

func (s *Staged) Info() Info { return s.info }

func (s *Staged) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return 0, os.ErrClosed
	}
	n, err := s.file.Read(p)
	if err != nil && err != io.EOF {
		return n, ErrStorage
	}
	return n, err
}

func (s *Staged) Seek(offset int64, whence int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return 0, os.ErrClosed
	}
	n, err := s.file.Seek(offset, whence)
	if err != nil {
		return 0, ErrStorage
	}
	return n, nil
}

// Close is idempotent after successful removal. If removal fails, retry Close;
// the root remains open for recovery. No recursive deletion is performed.
func (s *Staged) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.removed {
		return nil
	}
	var closeErr error
	if s.file != nil {
		closeErr = s.file.Close()
		s.file = nil
	}
	if err := s.root.Remove(s.name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrCleanup
	}
	s.removed = true
	rootErr := s.root.Close()
	if closeErr != nil || rootErr != nil {
		return ErrCleanup
	}
	return nil
}
