//go:build integration

package main

import (
	"context"
	"errors"
	"testing"

	"full-stack-file-vault.local/api/internal/files"
	"full-stack-file-vault.local/api/internal/upload"
	"github.com/jackc/pgx/v5"
)

func testUploadReceipts(t *testing.T, ctx context.Context, db *pgx.Conn, dsn, directory string) {
	f := newPublicationFixture(t, ctx, db, dsn, directory)
	identity, userID, _, _ := f.user(100)
	first, second := f.staged("retry-safe"), f.staged("retry-safe")
	key := "12345678-1234-4234-9234-123456789abc"
	type result struct {
		files []upload.PublishedFile
		err   error
	}
	results := make(chan result, 2)
	p1, p2 := f.publisher(f.pool, f.local), f.publisher(f.other, f.local)
	go func() { out, err := p1.Publish(identity, []*upload.Staged{first}, key); results <- result{out, err} }()
	go func() { out, err := p2.Publish(identity, []*upload.Staged{second}, key); results <- result{out, err} }()
	a, b := <-results, <-results
	if a.err != nil || b.err != nil || len(a.files) != 1 || len(b.files) != 1 || a.files[0].ID != b.files[0].ID {
		t.Fatalf("concurrent retry failed: %v %v", a.err, b.err)
	}
	assertUsage := func(want int64, wantFiles int) {
		t.Helper()
		var used int64
		var count int
		if err := db.QueryRow(ctx, "SELECT used_bytes,(SELECT count(*) FROM vault.files WHERE owner_id=$1) FROM vault.users WHERE id=$1", userID).Scan(&used, &count); err != nil {
			t.Fatal(err)
		}
		if used != want || count != wantFiles {
			t.Fatalf("usage=%d files=%d", used, count)
		}
	}
	assertUsage(10, 1)
	if _, err := p1.Publish(identity, []*upload.Staged{f.staged("different")}, key); !errors.Is(err, upload.ErrRetryConflict) {
		t.Fatal("changed retry accepted", err)
	}
	assertUsage(10, 1)
	other, _, _, _ := f.user(100)
	out, err := p1.Publish(other, []*upload.Staged{f.staged("retry-safe")}, key)
	if err != nil || out[0].ID == a.files[0].ID {
		t.Fatal("key must be owner scoped", err)
	}
	reader, err := files.NewStore(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	if err = reader.Delete(identity, a.files[0].ID); err != nil {
		t.Fatal(err)
	}
	assertUsage(0, 0)
	replay, err := p1.Publish(identity, []*upload.Staged{f.staged("retry-safe")}, key)
	if err != nil || replay[0].ID != a.files[0].ID {
		t.Fatal("deleted receipt must replay original result", err)
	}
	assertUsage(0, 0)
	// Receipt failure rolls back quota and files even if physical promotion happened.
	if _, err = db.Exec(ctx, "REVOKE INSERT ON vault.upload_receipts FROM vault_runtime"); err != nil {
		t.Fatal(err)
	}
	_, publishErr := p1.Publish(identity, []*upload.Staged{f.staged("rollback")}, "22345678-1234-4234-9234-123456789abc")
	_, restoreErr := db.Exec(ctx, "GRANT INSERT ON vault.upload_receipts TO vault_runtime")
	if restoreErr != nil {
		t.Fatal(restoreErr)
	}
	if !errors.Is(publishErr, upload.ErrPublication) {
		t.Fatal("receipt insert failure accepted", publishErr)
	}
	assertUsage(0, 0)
	if _, err = f.pool.Exec(ctx, "DELETE FROM vault.upload_receipts WHERE owner_id=$1", userID); err == nil {
		t.Fatal("runtime can discard retry protection")
	}
	if _, err = db.Exec(ctx, migrationSQL(t, "000014_upload_receipts.down.sql")); err == nil {
		t.Fatal("receipt rollback discarded protection")
	}
	if _, err = db.Exec(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
}
