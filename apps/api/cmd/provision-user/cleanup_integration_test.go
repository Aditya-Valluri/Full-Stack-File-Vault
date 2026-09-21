//go:build integration

package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"full-stack-file-vault.local/api/internal/cleanup"
	"full-stack-file-vault.local/api/internal/files"
	"full-stack-file-vault.local/api/internal/upload"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type cleanupStorage struct {
	base cleanup.Storage
	hook func(context.Context, string) error
}

func (s cleanupStorage) Remove(ctx context.Context, key string) error {
	if s.hook != nil {
		if err := s.hook(ctx, key); err != nil {
			return err
		}
	}
	return s.base.Remove(ctx, key)
}

func testCleanup(t *testing.T, ctx context.Context, admin *pgx.Conn, runtimeDSN, gcDSN, directory string) {
	f := newPublicationFixture(t, ctx, admin, runtimeDSN, directory)
	pool, err := pgxpool.New(ctx, gcDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	reader, err := files.NewStore(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	publisher := f.publisher(f.pool, f.local)
	collect := func(storage cleanup.Storage) *cleanup.Collector {
		t.Helper()
		c, err := cleanup.NewCollector(pool, storage, 100)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	run := func(c *cleanup.Collector) {
		t.Helper()
		if _, err := c.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	keyFor := func(id string) string {
		t.Helper()
		var key string
		if err := admin.QueryRow(ctx, "SELECT b.storage_key FROM vault.files f JOIN vault.blobs b ON b.id=f.blob_id WHERE f.id=$1", id).Scan(&key); err != nil {
			t.Fatal(err)
		}
		return key
	}
	retire := func(body string) (context.Context, string) {
		t.Helper()
		request, _, _, _ := f.user(1000)
		items, err := publisher.Publish(request, []*upload.Staged{f.staged(body)})
		if err != nil {
			t.Fatal(err)
		}
		key := keyFor(items[0].ID)
		if err := reader.Delete(request, items[0].ID); err != nil {
			t.Fatal(err)
		}
		exec("UPDATE vault.blobs SET gc_after=clock_timestamp()-interval '1 second' WHERE storage_key=$1", key)
		return request, key
	}
	t.Run("separate database privileges", func(t *testing.T) {
		if _, err := f.pool.Exec(ctx, "DELETE FROM vault.blobs WHERE false"); err == nil {
			t.Fatal("runtime can delete blobs")
		}
		if _, err := pool.Exec(ctx, "DELETE FROM vault.files WHERE false"); err == nil {
			t.Fatal("collector can delete user files")
		}
		if _, err := pool.Exec(ctx, "UPDATE vault.users SET used_bytes=0 WHERE false"); err == nil {
			t.Fatal("collector can change quota")
		}
	})
	t.Run("delayed removal cannot delete a replacement generation", func(t *testing.T) {
		body := "gc-delayed-generation"
		request, oldKey := retire(body)
		started, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		unblock := func() { once.Do(func() { close(release) }) }
		defer unblock()
		c := collect(cleanupStorage{base: f.local, hook: func(ctx context.Context, key string) error {
			if key == oldKey {
				close(started)
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		}})
		done := make(chan error, 1)
		go func() { _, err := c.RunOnce(ctx); done <- err }()
		select {
		case <-started:
		case err := <-done:
			t.Fatal("worker failed before remove", err)
		case <-time.After(10 * time.Second):
			t.Fatal("cleanup did not reach removal")
		}
		items, err := publisher.Publish(request, []*upload.Staged{f.staged(body)})
		if err != nil {
			t.Fatal(err)
		}
		newKey := keyFor(items[0].ID)
		if newKey == oldKey {
			t.Fatal("retired generation revived")
		}
		unblock()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("worker did not finish")
		}
		opened, err := f.local.Open(ctx, newKey, int64(len(body)))
		if err != nil {
			t.Fatal("replacement bytes removed", err)
		}
		_ = opened.Close()
		if _, err := f.local.Open(ctx, oldKey, int64(len(body))); err == nil {
			t.Fatal("old generation retained")
		}
	})
	t.Run("live references win over orphan candidates", func(t *testing.T) {
		request, _, _, _ := f.user(100)
		body := "gc-live-reference"
		items, err := publisher.Publish(request, []*upload.Staged{f.staged(body)})
		if err != nil {
			t.Fatal(err)
		}
		key := keyFor(items[0].ID)
		exec("UPDATE vault.object_candidates SET state='CLEANUP',next_attempt_at=clock_timestamp() WHERE storage_key=$1", key)
		run(collect(f.local))
		opened, err := f.local.Open(ctx, key, int64(len(body)))
		if err != nil {
			t.Fatal("referenced bytes removed", err)
		}
		_ = opened.Close()
		exec("UPDATE vault.object_candidates SET state='PUBLISHED' WHERE storage_key=$1", key)
	})
	t.Run("advisory lock defers retirement", func(t *testing.T) {
		body := "gc-held-digest"
		_, key := retire(body)
		tx, err := f.other.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		digest := sha256.Sum256([]byte(body))
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(binary.BigEndian.Uint64(digest[:8]))); err != nil {
			t.Fatal(err)
		}
		run(collect(f.local))
		opened, err := f.local.Open(ctx, key, int64(len(body)))
		if err != nil {
			t.Fatal("locked bytes removed", err)
		}
		_ = opened.Close()
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		run(collect(f.local))
		if _, err := f.local.Open(ctx, key, int64(len(body))); err == nil {
			t.Fatal("unlocked cleanup did not retry")
		}
	})
	t.Run("failed retirement never removes bytes", func(t *testing.T) {
		body := "gc-retirement-rollback"
		_, key := retire(body)
		// A referencing FK in an isolated test transaction simulates a database deletion failure.
		exec("CREATE TABLE vault.cleanup_test_guard(blob_id uuid REFERENCES vault.blobs(id))")
		defer exec("DROP TABLE vault.cleanup_test_guard")
		exec("INSERT INTO vault.cleanup_test_guard SELECT id FROM vault.blobs WHERE storage_key=$1", key)
		if _, err := collect(f.local).RunOnce(ctx); !errors.Is(err, cleanup.ErrCleanup) {
			t.Fatal("retirement failure hidden", err)
		}
		opened, err := f.local.Open(ctx, key, int64(len(body)))
		if err != nil {
			t.Fatal("uncommitted retirement removed bytes", err)
		}
		_ = opened.Close()
		exec("DELETE FROM vault.cleanup_test_guard")
		run(collect(f.local))
	})
	t.Run("physical failure retains durable retry", func(t *testing.T) {
		body := "gc-retry-removal"
		_, key := retire(body)
		c := collect(cleanupStorage{base: f.local, hook: func(ctx context.Context, k string) error {
			if k == key {
				if err := f.local.Remove(ctx, k); err != nil {
					return err
				}
				return errors.New("acknowledgement lost")
			}
			return nil
		}})
		if _, err := c.RunOnce(ctx); !errors.Is(err, cleanup.ErrCleanup) {
			t.Fatal("storage failure hidden", err)
		}
		var state string
		if err := admin.QueryRow(ctx, "SELECT state FROM vault.object_candidates WHERE storage_key=$1", key).Scan(&state); err != nil || state != "DELETING" {
			t.Fatal("retry intent lost", state, err)
		}
		exec("UPDATE vault.object_candidates SET next_attempt_at=clock_timestamp() WHERE storage_key=$1", key)
		run(collect(f.local))
	})
	t.Run("late orphan promotion is removed by retained tombstone", func(t *testing.T) {
		body := "gc-late-orphan"
		prepared, err := f.local.Prepare(ctx, f.staged(body))
		if err != nil {
			t.Fatal(err)
		}
		defer prepared.Close()
		key := "blob-" + strings.Repeat("9", 64)
		digest := sha256.Sum256([]byte(body))
		exec("INSERT INTO vault.object_candidates(storage_key,sha256,size_bytes,state,created_at) VALUES($1,$2,$3,'PENDING',clock_timestamp()-interval '11 minutes')", key, digest[:], len(body))
		run(collect(f.local))
		if err := prepared.Promote(ctx, key); err != nil {
			t.Fatal(err)
		}
		exec("UPDATE vault.object_candidates SET next_attempt_at=clock_timestamp() WHERE storage_key=$1", key)
		run(collect(f.local))
		if _, err := f.local.Open(ctx, key, int64(len(body))); err == nil {
			t.Fatal("late orphan survived retry")
		}
	})
	t.Run("concurrent workers are idempotent", func(t *testing.T) {
		body := "gc-multiple-workers"
		_, key := retire(body)
		c := collect(f.local)
		done := make(chan error, 2)
		for i := 0; i < 2; i++ {
			go func() { _, err := c.RunOnce(ctx); done <- err }()
		}
		for i := 0; i < 2; i++ {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
		if _, err := f.local.Open(ctx, key, int64(len(body))); err == nil {
			t.Fatal("concurrent cleanup left bytes")
		}
	})
}
