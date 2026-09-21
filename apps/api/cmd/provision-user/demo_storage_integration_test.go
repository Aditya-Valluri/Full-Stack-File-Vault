//go:build integration

package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"file-vault.local/api/internal/demostore"
	"file-vault.local/api/internal/upload"
	"github.com/jackc/pgx/v5"
)

func testDemoStorage(t *testing.T, ctx context.Context, admin *pgx.Conn, runtimeDSN, gcDSN string) {
	if _, err := admin.Exec(ctx, demostore.Schema); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, demostore.Schema); err != nil {
		t.Fatal("repeat schema:", err)
	}
	store, err := demostore.Open(ctx, runtimeDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	gc, err := demostore.Open(ctx, gcDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer gc.Close()
	f := newPublicationFixture(t, ctx, admin, runtimeDSN, t.TempDir())
	owner, user, _, _ := f.user(10000000)
	publisher, err := upload.NewPublisher(f.pool, store, f.logger)
	if err != nil {
		t.Fatal(err)
	}
	staged := f.staged("database backed demo bytes")
	retryKey := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	result, err := publisher.Publish(owner, []*upload.Staged{staged, staged}, retryKey)
	if err != nil || len(result) != 2 {
		t.Fatalf("publish: %v", err)
	}
	replay, err := publisher.Publish(owner, []*upload.Staged{staged, staged}, retryKey)
	if err != nil || replay[0].ID != result[0].ID {
		t.Fatal("receipt replay failed", err)
	}
	var count int
	var used int64
	var key string
	if err = admin.QueryRow(ctx, "SELECT count(*),COALESCE(sum(octet_length(content)),0) FROM vault_demo.objects").Scan(&count, &used); err != nil || count != 1 || used != staged.Info().SizeBytes {
		t.Fatal("deduplication failed", err)
	}
	if err = admin.QueryRow(ctx, "SELECT b.storage_key FROM vault.files f JOIN vault.blobs b ON b.id=f.blob_id WHERE f.id=$1", result[0].ID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	handle, err := store.Open(ctx, key, used)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(handle)
	handle.Close()
	if err != nil || string(data) != "database backed demo bytes" {
		t.Fatal("bytes differ")
	}
	if err = store.Verify(ctx, key, used+1); err == nil {
		t.Fatal("size mismatch accepted")
	}
	if err = gc.Remove(ctx, key); err == nil {
		t.Fatal("live generation removed")
	}
	// Least privilege: runtime cannot delete bytes or alter the capacity counter;
	// collector can delete fenced keys, but cannot read or publish content.
	runtimeConn, err := pgx.Connect(ctx, runtimeDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer runtimeConn.Close(ctx)
	gcConn, err := pgx.Connect(ctx, gcDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer gcConn.Close(ctx)
	for _, statement := range []string{
		"DELETE FROM vault_demo.objects",
		"UPDATE vault_demo.capacity SET used_bytes=0",
		"SELECT vault_demo.remove_object('" + key + "')",
	} {
		if _, err = runtimeConn.Exec(ctx, statement); err == nil {
			t.Fatal("runtime privilege leak")
		}
	}
	if _, err = gcConn.Exec(ctx, "SELECT content FROM vault_demo.objects"); err == nil {
		t.Fatal("collector read leak")
	}
	if _, err = gcConn.Exec(ctx, "SELECT vault_demo.put_object('"+key+"',''::bytea,''::bytea)"); err == nil {
		t.Fatal("collector write leak")
	}

	// File-size boundary is enforced by the adapter even outside GraphQL.
	oversized := f.staged(strings.Repeat("x", 10000001))
	if _, err = store.Prepare(ctx, oversized); !errors.Is(err, upload.ErrTooLarge) {
		t.Fatal("oversized demo object accepted", err)
	}
	oversized.Close()
	exactInput := f.staged(strings.Repeat("y", 10000000))
	exactPrepared, err := store.Prepare(ctx, exactInput)
	if err != nil {
		t.Fatal("exact file-size bound rejected", err)
	}
	exactKey := "blob-" + strings.Repeat("e", 64)
	exactHash := exactInput.Info().SHA256
	if _, err = admin.Exec(ctx, "INSERT INTO vault.object_candidates(storage_key,sha256,size_bytes) VALUES($1,$2,10000000)", exactKey, exactHash[:]); err != nil {
		t.Fatal(err)
	}
	exactInput.Close() // Preparation owns a verified copy independent of staging.
	if err = exactPrepared.Promote(ctx, exactKey); err != nil {
		t.Fatal("maximum-size publication failed", err)
	}
	exactPrepared.Close()
	exactInput.Close()
	exactHandle, err := store.Open(ctx, exactKey, 10000000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = exactHandle.Seek(9999999, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	last := make([]byte, 1)
	if _, err = io.ReadFull(exactHandle, last); err != nil || last[0] != 'y' {
		t.Fatal("seekable max-size content failed", err)
	}
	exactHandle.Close()
	if _, err = admin.Exec(ctx, "UPDATE vault.object_candidates SET state='DELETING' WHERE storage_key=$1", exactKey); err != nil {
		t.Fatal(err)
	}
	if err = gc.Remove(ctx, exactKey); err != nil {
		t.Fatal(err)
	}
	// Whole-batch rollback: force enough capacity for the first unique object only.
	// Whichever digest sorts first fits; second promotion must fail.
	a, b := f.staged("unique-A"), f.staged("unique-B")
	if _, err = admin.Exec(ctx, "UPDATE vault_demo.capacity SET used_bytes=99999992"); err != nil {
		t.Fatal(err)
	}
	failedOwner, failedUser, _, _ := f.user(10000000)
	_, err = publisher.Publish(failedOwner, []*upload.Staged{a, b})
	if !errors.Is(err, upload.ErrDemoCapacity) {
		t.Fatal("capacity error not preserved", err)
	}
	var charged int64
	var files int
	if err = admin.QueryRow(ctx, "SELECT used_bytes,(SELECT count(*) FROM vault.files WHERE owner_id=$1) FROM vault.users WHERE id=$1", failedUser).Scan(&charged, &files); err != nil || charged != 0 || files != 0 {
		t.Fatal("failed batch charged user", err)
	}
	// Restore the operator-only test adjustment. Production counters cannot be
	// changed by either child role; real accounting remains transactionally exact.
	if _, err = admin.Exec(ctx, "UPDATE vault_demo.capacity SET used_bytes=(SELECT COALESCE(sum(octet_length(content)),0) FROM vault_demo.objects)"); err != nil {
		t.Fatal(err)
	}

	// Concurrent admission across independent connections cannot oversubscribe.
	if _, err = admin.Exec(ctx, "UPDATE vault_demo.capacity SET used_bytes=99999999"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	outcomes := make(chan error, 4)
	for i := 0; i < 4; i++ {
		candidateKey := "blob-" + fmt.Sprintf("%064x", i+100)
		value := string(rune('a' + i))
		digest := sha256.Sum256([]byte(value))
		if _, err = admin.Exec(ctx, "INSERT INTO vault.object_candidates(storage_key,sha256,size_bytes) VALUES($1,$2,1)", candidateKey, digest[:]); err != nil {
			t.Fatal(err)
		}
		input := f.staged(value)
		prepared, err := store.Prepare(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() { defer wg.Done(); defer prepared.Close(); outcomes <- prepared.Promote(ctx, candidateKey) }()
	}
	wg.Wait()
	close(outcomes)
	accepted, limited := 0, 0
	for err := range outcomes {
		if err == nil {
			accepted++
		} else if errors.Is(err, upload.ErrDemoCapacity) {
			limited++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 1 || limited != 3 {
		t.Fatalf("concurrent cap: accepted=%d limited=%d", accepted, limited)
	}
	if _, err = admin.Exec(ctx, "UPDATE vault_demo.capacity SET used_bytes=(SELECT COALESCE(sum(octet_length(content)),0) FROM vault_demo.objects)"); err != nil {
		t.Fatal(err)
	}

	// Existing keys are immutable, even if the original intent is still pending.
	var immutableKey string
	var immutableData []byte
	if err = admin.QueryRow(ctx, "SELECT storage_key,content FROM vault_demo.objects WHERE octet_length(content)=1 LIMIT 1").Scan(&immutableKey, &immutableData); err != nil {
		t.Fatal(err)
	}
	prepared, err := store.Prepare(ctx, f.staged(string(immutableData)))
	if err != nil {
		t.Fatal(err)
	}
	if err = prepared.Promote(ctx, immutableKey); err == nil {
		t.Fatal("existing generation overwritten")
	}
	prepared.Close()

	// Simulate collector fencing on this test's unreferenced object, then remove twice.
	if _, err = admin.Exec(ctx, "UPDATE vault.object_candidates SET state='DELETING' WHERE storage_key=$1", immutableKey); err != nil {
		t.Fatal(err)
	}
	if err = gc.Remove(ctx, immutableKey); err != nil {
		t.Fatal(err)
	}
	if err = gc.Remove(ctx, immutableKey); err != nil {
		t.Fatal("non-idempotent removal", err)
	}
	if err = store.Verify(ctx, immutableKey, 1); !errors.Is(err, upload.ErrObjectMissing) {
		t.Fatal("object still exists")
	}
	var exact bool
	if err = admin.QueryRow(ctx, "SELECT used_bytes=(SELECT COALESCE(sum(octet_length(content)),0) FROM vault_demo.objects) AND object_count=(SELECT count(*) FROM vault_demo.objects) FROM vault_demo.capacity").Scan(&exact); err != nil || !exact {
		t.Fatal("capacity accounting drift", err)
	}
	if err = admin.QueryRow(ctx, "SELECT used_bytes FROM vault.users WHERE id=$1", user).Scan(&charged); err != nil || charged != 2*used {
		t.Fatal("logical quota/retry accounting changed", err)
	}
}
