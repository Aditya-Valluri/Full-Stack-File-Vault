// Package demostore stores bounded immutable generations in PostgreSQL for the
// temporary Render demo. It is not a replacement for production object storage.
package demostore

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"full-stack-file-vault.local/api/internal/upload"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Schema is installed transactionally by the demo operator after normal migrations.
//
//go:embed schema.sql
var Schema string

const maxObjectBytes int64 = 10000000
const maxPreparedBytes int64 = 22000000

// BlobStore is the demo-only PostgreSQL byte adapter. Production never selects it implicitly.
type BlobStore struct {
	pool          *pgxpool.Pool
	slots         chan struct{}
	preparedMu    sync.Mutex
	preparedBytes int64
}

// Open uses a separate, small pool: publication/content authorization can already
// hold a connection and row locks in the application pool when byte I/O begins.
func Open(ctx context.Context, dsn string) (*BlobStore, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, upload.ErrStorage
	}
	cfg.MaxConns = 2
	cfg.MinConns = 0
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "3000"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, upload.ErrStorage
	}
	var role string
	var installed bool
	err = pool.QueryRow(ctx, "SELECT current_user,to_regclass('vault_demo.objects') IS NOT NULL").Scan(&role, &installed)
	if err != nil || !installed || (role != "vault_runtime" && role != "vault_gc") {
		pool.Close()
		return nil, upload.ErrStorage
	}
	return &BlobStore{pool: pool, slots: make(chan struct{}, 2)}, nil
}
func (s *BlobStore) Close() error { s.pool.Close(); return nil }
func validKey(key string) bool {
	if len(key) != 69 || !strings.HasPrefix(key, "blob-") {
		return false
	}
	for _, c := range key[5:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func (s *BlobStore) acquire(ctx context.Context) error {
	select {
	case s.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *BlobStore) release() { <-s.slots }

// Prepare copies and verifies bytes before the publisher takes database locks.
// The transport bounds each batch; this independent 22 MB reservation also prevents
// direct/internal callers from retaining unbounded prepared objects.
func (s *BlobStore) Prepare(ctx context.Context, source *upload.Staged) (result upload.PreparedObject, err error) {
	if source == nil {
		return nil, upload.ErrInvalidInput
	}
	info := source.Info()
	if info.SizeBytes < 0 || info.SizeBytes > maxObjectBytes {
		return nil, upload.ErrTooLarge
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	s.preparedMu.Lock()
	if info.SizeBytes > maxPreparedBytes-s.preparedBytes {
		s.preparedMu.Unlock()
		return nil, upload.ErrStorage
	}
	s.preparedBytes += info.SizeBytes
	s.preparedMu.Unlock()
	var data []byte
	defer func() {
		if result == nil {
			clear(data)
			s.releasePrepared(info.SizeBytes)
		}
	}()
	if _, err = source.Seek(0, io.SeekStart); err != nil {
		return nil, upload.ErrStorage
	}
	data, err = io.ReadAll(io.LimitReader(source, info.SizeBytes+1))
	if err != nil || int64(len(data)) != info.SizeBytes || sha256.Sum256(data) != info.SHA256 {
		return nil, upload.ErrIntegrity
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return &prepared{store: s, data: data, digest: info.SHA256}, nil
}
func (s *BlobStore) releasePrepared(size int64) {
	s.preparedMu.Lock()
	defer s.preparedMu.Unlock()
	s.preparedBytes -= size
}

type prepared struct {
	mu               sync.Mutex
	store            *BlobStore
	data             []byte
	digest           [32]byte
	closed, promoted bool
}

func (p *prepared) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		p.store.releasePrepared(int64(len(p.data)))
		clear(p.data)
		p.data = nil
	}
	return nil
}
func (p *prepared) Promote(ctx context.Context, key string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.promoted || !validKey(key) {
		return upload.ErrStorage
	}
	if err := p.store.acquire(ctx); err != nil {
		return err
	}
	defer p.store.release()
	// Only bounded database I/O remains while publication locks are held.
	_, err := p.store.pool.Exec(ctx, "SELECT vault_demo.put_object($1,$2,$3)", key, p.data, p.digest[:])
	if err != nil {
		var dbErr *pgconn.PgError
		if errors.As(err, &dbErr) && dbErr.Code == "PV001" {
			return upload.ErrDemoCapacity
		}
		return upload.ErrStorage
	}
	p.promoted = true
	return nil
}

func (s *BlobStore) Verify(ctx context.Context, key string, size int64) error {
	if !validKey(key) || size < 0 || size > maxObjectBytes {
		return upload.ErrStorage
	}
	var actual int64
	err := s.pool.QueryRow(ctx, "SELECT octet_length(content) FROM vault_demo.objects WHERE storage_key=$1", key).Scan(&actual)
	if errors.Is(err, pgx.ErrNoRows) {
		return upload.ErrObjectMissing
	}
	if err != nil || actual != size {
		return upload.ErrStorage
	}
	return nil
}
func (s *BlobStore) Open(ctx context.Context, key string, size int64) (io.ReadSeekCloser, error) {
	if !validKey(key) || size < 0 || size > maxObjectBytes {
		return nil, upload.ErrStorage
	}
	if err := s.acquire(ctx); err != nil {
		return nil, err
	}
	var data, digest []byte
	err := s.pool.QueryRow(ctx, "SELECT content,sha256 FROM vault_demo.objects WHERE storage_key=$1 AND octet_length(content)=$2", key, size).Scan(&data, &digest)
	if err != nil {
		s.release()
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, upload.ErrObjectMissing
		}
		return nil, upload.ErrStorage
	}
	hash := sha256.Sum256(data)
	if int64(len(data)) != size || !bytes.Equal(hash[:], digest) {
		clear(data)
		s.release()
		return nil, upload.ErrIntegrity
	}
	// Memory and admission remain owned until the authorized transfer closes.
	return &reader{Reader: bytes.NewReader(data), data: data, release: s.release}, nil
}
func (s *BlobStore) Remove(ctx context.Context, key string) error {
	if !validKey(key) {
		return upload.ErrStorage
	}
	if _, err := s.pool.Exec(ctx, "SELECT vault_demo.remove_object($1)", key); err != nil {
		return upload.ErrStorage
	}
	return nil
}

type reader struct {
	*bytes.Reader
	data    []byte
	release func()
}

func (r *reader) Close() error {
	if r.release != nil {
		clear(r.data)
		r.Reader.Reset(nil)
		r.data = nil
		r.release()
		r.release = nil
	}
	return nil
}
