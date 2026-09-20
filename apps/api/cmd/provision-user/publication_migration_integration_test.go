//go:build integration

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func migrationSQL(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "migrations", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Runs only inside the disposable database owned by TestProvisionCommandIntegration.
func testPublicationMigration(t *testing.T, ctx context.Context, db *pgx.Conn) {
	up := migrationSQL(t, "000008_file_publication.up.sql")
	down := migrationSQL(t, "000008_file_publication.down.sql")
	exec := func(sql string) {
		t.Helper()
		if _, err := db.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	exec(up)
	exec(down) // Empty-vault rollback restores the pre-publication schema.
	exec("INSERT INTO vault.users(id,quota_bytes) VALUES('00000000-0000-4000-8000-000000000001',3)")
	exec("INSERT INTO vault.blobs(id,sha256,size_bytes,storage_key) VALUES('00000000-0000-4000-8000-000000000002',decode(repeat('ab',32),'hex'),4,'migration-test')")
	exec("INSERT INTO vault.files(owner_id,blob_id,original_name) VALUES('00000000-0000-4000-8000-000000000001','00000000-0000-4000-8000-000000000002','fixture')")
	if _, err := db.Exec(ctx, up); err == nil {
		t.Fatal("over-quota migration accepted")
	}
	exec("ROLLBACK")
	var exists bool
	if err := db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='vault' AND table_name='users' AND column_name='used_bytes')").Scan(&exists); err != nil || exists {
		t.Fatal("failed migration left partial schema")
	}
	exec("UPDATE vault.users SET quota_bytes=20000000")
	exec(up)
	var used int64
	if err := db.QueryRow(ctx, "SELECT used_bytes FROM vault.users").Scan(&used); err != nil || used != 4 {
		t.Fatal("incorrect usage backfill")
	}
	if _, err := db.Exec(ctx, down); err == nil {
		t.Fatal("rollback discarded referenced data")
	}
	exec("ROLLBACK")
	// Cleanup is confined to the isolated migration fixture before account tests.
	exec("DELETE FROM vault.files")
	exec("DELETE FROM vault.blobs")
	exec("DELETE FROM vault.users")
	for _, query := range []string{
		"UPDATE vault.users SET quota_bytes=1",
		"UPDATE vault.users SET role='ADMIN'",
		"DELETE FROM vault.blobs",
		"DELETE FROM vault.object_candidates",
	} {
		tx, err := db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, "SET LOCAL ROLE vault_runtime"); err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, query)
		_ = tx.Rollback(ctx)
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("unexpected privilege for %s: %v", query, err)
		}
	}
}
