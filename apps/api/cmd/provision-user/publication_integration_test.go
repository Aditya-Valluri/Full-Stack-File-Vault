//go:build integration

package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"full-stack-file-vault.local/api/internal/auth"
	"full-stack-file-vault.local/api/internal/upload"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type publicationFixture struct {
	t           *testing.T
	ctx         context.Context
	admin       *pgx.Conn
	pool, other *pgxpool.Pool
	sessions    *auth.SessionStore
	browser     *auth.BrowserSecurity
	local       *upload.LocalStore
	dir         string
	logger      *slog.Logger
}

func newPublicationFixture(t *testing.T, ctx context.Context, admin *pgx.Conn, dsn, storageDirectory string) *publicationFixture {
	t.Helper()
	f := &publicationFixture{t: t, ctx: ctx, admin: admin, dir: storageDirectory, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	var err error
	f.pool, err = pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.pool.Close)
	f.other, err = pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.other.Close)
	f.sessions, err = auth.NewSessionStore(f.pool, 24*time.Hour, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	f.browser, err = auth.NewBrowserSecurity(auth.BrowserConfig{Origin: "https://vault.example.com"}, f.sessions)
	if err != nil {
		t.Fatal(err)
	}
	f.local, err = upload.NewLocalStore(f.dir, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.local.Close() })
	return f
}

func (f *publicationFixture) user(quota int64) (context.Context, string, string, auth.Session) {
	f.t.Helper()
	var id string
	if err := f.admin.QueryRow(f.ctx, "INSERT INTO vault.users(quota_bytes) VALUES($1) RETURNING id::text", quota).Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	token, state, err := f.sessions.Create(f.ctx, id)
	if err != nil {
		f.t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "https://vault.example.com/graphql", nil).WithContext(f.ctx)
	req.Header.Set("Origin", "https://vault.example.com")
	req.Header.Set("X-CSRF-Token", state.CSRFToken)
	req.AddCookie(&http.Cookie{Name: "__Host-vault_session", Value: token})
	var captured context.Context
	f.browser.Wrap(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { captured = r.Context() })).ServeHTTP(httptest.NewRecorder(), req)
	if captured == nil {
		f.t.Fatal("cannot capture authenticated request context")
	}
	return captured, id, token, state
}

func (f *publicationFixture) staged(data string) *upload.Staged {
	f.t.Helper()
	s, err := upload.Stage(f.ctx, f.t.TempDir(), strings.NewReader(data), upload.Metadata{Name: "example.txt", DeclaredMIME: "image/png"}, int64(len(data))+1)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() {
		if err := s.Close(); err != nil {
			f.t.Error(err)
		}
	})
	return s
}
func (f *publicationFixture) publisher(pool *pgxpool.Pool, store upload.PublicationStore) *upload.Publisher {
	f.t.Helper()
	p, err := upload.NewPublisher(pool, store, f.logger)
	if err != nil {
		f.t.Fatal(err)
	}
	return p
}
func (f *publicationFixture) usage(p *upload.Publisher, ctx context.Context, want int64) {
	f.t.Helper()
	u, err := p.Reconcile(ctx)
	if err != nil || u.StoredBytes != want || u.ActualBytes != want {
		f.t.Fatalf("usage: %+v, %v; want %d", u, err, want)
	}
}

type faultStore struct {
	upload.PublicationStore
	after bool
	hook  func()
}
type faultPrepared struct {
	upload.PreparedObject
	after bool
	hook  func()
}

func (s faultStore) Prepare(ctx context.Context, staged *upload.Staged) (upload.PreparedObject, error) {
	p, err := s.PublicationStore.Prepare(ctx, staged)
	if err != nil {
		return nil, err
	}
	return faultPrepared{p, s.after, s.hook}, nil
}
func (p faultPrepared) Promote(ctx context.Context, key string) error {
	if p.after {
		if err := p.PreparedObject.Promote(ctx, key); err != nil {
			return err
		}
	}
	if p.hook != nil {
		p.hook()
		return nil
	}
	return upload.ErrStorage
}

func testPublication(t *testing.T, ctx context.Context, admin *pgx.Conn, dsn, storageDirectory string) {
	f := newPublicationFixture(t, ctx, admin, dsn, storageDirectory)
	publisher := f.publisher(f.pool, f.local)
	t.Run("configured quota above default", func(t *testing.T) {
		request, _, _, _ := f.user(20000000)
		const size = 10000001
		if _, err := publisher.Publish(request, []*upload.Staged{f.staged(strings.Repeat("q", size))}); err != nil {
			t.Fatal("higher configured quota rejected", err)
		}
		f.usage(publisher, request, size)
	})
	t.Run("duplicates charge each logical file and exact boundary", func(t *testing.T) {
		request, _, _, _ := f.user(8)
		files, err := publisher.Publish(request, []*upload.Staged{f.staged("same"), f.staged("same")})
		if err != nil || len(files) != 2 || files[0].ID == files[1].ID {
			t.Fatalf("publish: %v", err)
		}
		if files[0].DetectedMIME == "image/png" {
			t.Fatal("trusted client MIME")
		}
		f.usage(publisher, request, 8)
		if _, err = publisher.Publish(request, []*upload.Staged{f.staged("x")}); !errors.Is(err, upload.ErrQuotaExceeded) {
			t.Fatalf("quota bypass: %v", err)
		}
		if _, err = publisher.Publish(request, []*upload.Staged{f.staged("")}); err != nil {
			t.Fatal(err)
		}
		f.usage(publisher, request, 8)
	})
	t.Run("cross user duplicate races use one generation", func(t *testing.T) {
		a, _, _, _ := f.user(100)
		b, _, _, _ := f.user(100)
		inputs := []*upload.Staged{f.staged("cross-user"), f.staged("cross-user")}
		publishers := []*upload.Publisher{publisher, f.publisher(f.other, f.local)}
		contexts := []context.Context{a, b}
		start := make(chan struct{})
		results := make(chan error, 2)
		for i := range 2 {
			go func(i int) {
				<-start
				_, err := publishers[i].Publish(contexts[i], []*upload.Staged{inputs[i]})
				results <- err
			}(i)
		}
		close(start)
		for range 2 {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
		}
		hash := sha256.Sum256([]byte("cross-user"))
		var count int
		if err := admin.QueryRow(ctx, "SELECT count(*) FROM vault.blobs WHERE sha256=$1", hash[:]).Scan(&count); err != nil || count != 1 {
			t.Fatal("dedup failed")
		}
		f.usage(publisher, a, 10)
		f.usage(publisher, b, 10)
	})
	t.Run("same user concurrent quota and atomic batch", func(t *testing.T) {
		request, _, _, _ := f.user(5)
		inputs := []*upload.Staged{f.staged("abc"), f.staged("def")}
		publishers := []*upload.Publisher{publisher, f.publisher(f.other, f.local)}
		results := make(chan error, 2)
		start := make(chan struct{})
		for i := range 2 {
			go func(i int) {
				<-start
				_, err := publishers[i].Publish(request, []*upload.Staged{inputs[i]})
				results <- err
			}(i)
		}
		close(start)
		ok, limited := 0, 0
		for range 2 {
			err := <-results
			if err == nil {
				ok++
			} else if errors.Is(err, upload.ErrQuotaExceeded) {
				limited++
			} else {
				t.Fatal(err)
			}
		}
		if ok != 1 || limited != 1 {
			t.Fatal("quota race admitted incorrect requests")
		}
		f.usage(publisher, request, 3)
		batch, _, _, _ := f.user(5)
		if _, err := publisher.Publish(batch, []*upload.Staged{f.staged("123"), f.staged("456")}); !errors.Is(err, upload.ErrQuotaExceeded) {
			t.Fatal(err)
		}
		f.usage(publisher, batch, 0)
	})
	t.Run("before and after promotion failures retain recovery intent", func(t *testing.T) {
		for _, after := range []bool{false, true} {
			request, _, _, _ := f.user(100)
			p := f.publisher(f.pool, faultStore{PublicationStore: f.local, after: after})
			if _, err := p.Publish(request, []*upload.Staged{f.staged("failure-content")}); !errors.Is(err, upload.ErrStorage) {
				t.Fatal(err)
			}
			f.usage(publisher, request, 0)
		}
		hash := sha256.Sum256([]byte("failure-content"))
		rows, err := admin.Query(ctx, "SELECT storage_key,state FROM vault.object_candidates WHERE sha256=$1", hash[:])
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		intents, objects := 0, 0
		for rows.Next() {
			var key, state string
			if err := rows.Scan(&key, &state); err != nil {
				t.Fatal(err)
			}
			if state != "PENDING" {
				t.Fatal("failed publication lost pending intent")
			}
			intents++
			if _, err := os.Stat(filepath.Join(f.dir, key)); err == nil {
				objects++
			}
		}
		if rows.Err() != nil || intents != 2 || objects != 1 {
			t.Fatalf("recovery intents=%d objects=%d", intents, objects)
		}
	})
	t.Run("authorization rechecked after staging", func(t *testing.T) {
		request, id, token, _ := f.user(100)
		if err := f.sessions.Revoke(ctx, token); err != nil {
			t.Fatal(err)
		}
		if _, err := publisher.Publish(request, []*upload.Staged{f.staged("revoked")}); !errors.Is(err, auth.ErrUnauthenticated) {
			t.Fatal("stale session published", err)
		}
		disabled, user, _, _ := f.user(100)
		if _, err := admin.Exec(ctx, "UPDATE vault.users SET disabled_at=clock_timestamp() WHERE id=$1", user); err != nil {
			t.Fatal(err)
		}
		if _, err := publisher.Publish(disabled, []*upload.Staged{f.staged("disabled")}); !errors.Is(err, auth.ErrUnauthenticated) {
			t.Fatal("disabled user published", err)
		}
		var usage int64
		if err := admin.QueryRow(ctx, "SELECT used_bytes FROM vault.users WHERE id=$1", id).Scan(&usage); err != nil || usage != 0 {
			t.Fatal("failed auth charged quota")
		}
		if _, err := publisher.Publish(ctx, []*upload.Staged{f.staged("anonymous")}); !errors.Is(err, auth.ErrUnauthenticated) {
			t.Fatal("anonymous published")
		}
	})
	t.Run("missing active object fails closed", func(t *testing.T) {
		request, _, _, _ := f.user(100)
		data := "missing-active"
		if _, err := publisher.Publish(request, []*upload.Staged{f.staged(data)}); err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256([]byte(data))
		var key string
		if err := admin.QueryRow(ctx, "SELECT storage_key FROM vault.blobs WHERE sha256=$1", hash[:]).Scan(&key); err != nil {
			t.Fatal(err)
		}
		// Delete one exact test-owned object to simulate storage loss.
		if err := os.Remove(filepath.Join(f.dir, key)); err != nil {
			t.Fatal(err)
		}
		if _, err := publisher.Publish(request, []*upload.Staged{f.staged(data)}); !errors.Is(err, upload.ErrIntegrity) {
			t.Fatal("missing active content reused", err)
		}
		f.usage(publisher, request, int64(len(data)))
	})
	t.Run("transaction rollback after promotion", func(t *testing.T) {
		request, _, _, _ := f.user(100)
		// A database constraint failure AFTER promotion must not leave logical rows.
		if _, err := admin.Exec(ctx, "ALTER TABLE vault.files ADD CONSTRAINT test_reject_file CHECK(original_name <> 'example.txt') NOT VALID"); err != nil {
			t.Fatal(err)
		}
		_, err := publisher.Publish(request, []*upload.Staged{f.staged("post-promotion-sql-failure")})
		_, dropErr := admin.Exec(ctx, "ALTER TABLE vault.files DROP CONSTRAINT test_reject_file")
		if dropErr != nil {
			t.Fatal(dropErr)
		}
		if !errors.Is(err, upload.ErrPublication) {
			t.Fatal("expected database publication failure", err)
		}
		f.usage(publisher, request, 0)
	})
	t.Run("pending generations revive or replace missing bytes", func(t *testing.T) {
		for _, missing := range []bool{false, true} {
			request, id, _, _ := f.user(100)
			data := "pending-present"
			if missing {
				data = "pending-missing"
			}
			files, err := publisher.Publish(request, []*upload.Staged{f.staged(data)})
			if err != nil {
				t.Fatal(err)
			}
			var blobID, oldKey string
			if err := admin.QueryRow(ctx, "SELECT b.id::text,b.storage_key FROM vault.blobs b JOIN vault.files f ON f.blob_id=b.id WHERE f.id=$1", files[0].ID).Scan(&blobID, &oldKey); err != nil {
				t.Fatal(err)
			}
			// Model a committed last-reference deletion, using only this test-owned file.
			tx, err := admin.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, "DELETE FROM vault.files WHERE id=$1", files[0].ID); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, "UPDATE vault.users SET used_bytes=0 WHERE id=$1", id); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, "UPDATE vault.blobs SET state='GC_PENDING',gc_after=clock_timestamp() WHERE id=$1", blobID); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if missing {
				if err = os.Remove(filepath.Join(f.dir, oldKey)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = publisher.Publish(request, []*upload.Staged{f.staged(data)}); err != nil {
				t.Fatal(err)
			}
			var newKey, state string
			if err := admin.QueryRow(ctx, "SELECT storage_key,state FROM vault.blobs WHERE id=$1", blobID).Scan(&newKey, &state); err != nil {
				t.Fatal(err)
			}
			if state != "ACTIVE" || (missing && newKey == oldKey) || (!missing && newKey != oldKey) {
				t.Fatal("unsafe pending-generation reuse")
			}
			if err := f.local.Verify(ctx, newKey, int64(len(data))); err != nil {
				t.Fatal(err)
			}
			f.usage(publisher, request, int64(len(data)))
		}
	})

	t.Run("reconciliation reports drift", func(t *testing.T) {
		request, id, _, _ := f.user(20000000)
		if _, err := admin.Exec(ctx, "UPDATE vault.users SET used_bytes=1 WHERE id=$1", id); err != nil {
			t.Fatal(err)
		}
		u, err := publisher.Reconcile(request)
		if err != nil || u.StoredBytes != 1 || u.ActualBytes != 0 || u.QuotaBytes != 20000000 {
			t.Fatal("drift hidden", u, err)
		}
	})
}
