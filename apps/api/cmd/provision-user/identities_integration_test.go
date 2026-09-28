//go:build integration

package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Apply after legacy accounts exist, then leave the migration installed so every
// existing session, login, file-isolation and admin regression runs against it.
func testIdentityFoundation(t *testing.T, ctx context.Context, admin *pgx.Conn, dsn string) {
	snapshot := func() string {
		t.Helper()
		var value string
		if err := admin.QueryRow(ctx, `SELECT md5(COALESCE(string_agg(u.id::text||':'||c.login_name||':'||c.password_hash||':'||u.role||':'||u.quota_bytes::text, ',' ORDER BY u.id),'')) FROM vault.users u JOIN vault.credentials c ON c.user_id=u.id`).Scan(&value); err != nil {
			t.Fatal("legacy snapshot failed")
		}
		return value
	}
	before := snapshot()
	for _, file := range []string{"000016_account_identities.up.sql", "000016_account_identities.down.sql", "000016_account_identities.up.sql"} {
		if _, err := admin.Exec(ctx, migrationSQL(t, file)); err != nil {
			t.Fatalf("identity migration %s failed: %v", file, err)
		}
	}
	if before != snapshot() {
		t.Fatal("identity migration changed legacy accounts")
	}
	var populated int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM vault.users WHERE email_address IS NOT NULL OR email_normalized IS NOT NULL OR email_verified_at IS NOT NULL`).Scan(&populated); err != nil || populated != 0 {
		t.Fatal("legacy emails were inferred")
	}
	runtime, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal("runtime connection failed")
	}
	defer runtime.Close(ctx)
	for _, table := range []string{"vault.users", "vault.credentials", "vault.user_identities"} {
		var allowed bool
		if err = runtime.QueryRow(ctx, `SELECT has_table_privilege(current_user,$1,'INSERT')`, table).Scan(&allowed); err != nil || allowed {
			t.Fatal("runtime gained account insertion privileges")
		}
	}
	var googleUser, passwordUser string
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = tx.QueryRow(ctx, `INSERT INTO vault.users DEFAULT VALUES RETURNING id::text`).Scan(&googleUser); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO vault.users DEFAULT VALUES RETURNING id::text`).Scan(&passwordUser); err != nil {
		t.Fatal(err)
	}
	expectSQLState := func(statement string, code string, args ...any) {
		t.Helper()
		savepoint, err := tx.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = savepoint.Exec(ctx, statement, args...)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != code {
			t.Fatalf("expected SQLSTATE %s", code)
		}
		if err = savepoint.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE vault.users SET email_address='Person@example.com',email_normalized='person@example.com',email_verified_at=clock_timestamp() WHERE id=$1`, googleUser); err != nil {
		t.Fatal(err)
	}
	expectSQLState(`UPDATE vault.users SET email_address='person@example.com',email_normalized='person@example.com',email_verified_at=clock_timestamp() WHERE id=$1`, "23505", passwordUser)
	expectSQLState(`UPDATE vault.users SET email_address='unverified@example.com' WHERE id=$1`, "23514", passwordUser)
	expectSQLState(`UPDATE vault.users SET email_normalized='PERSON@example.com' WHERE id=$1`, "23514", googleUser)
	if _, err = tx.Exec(ctx, `INSERT INTO vault.user_identities(user_id,provider,provider_subject) VALUES($1,'google','synthetic-google-sub')`, googleUser); err != nil {
		t.Fatal(err)
	}
	expectSQLState(`INSERT INTO vault.user_identities(user_id,provider,provider_subject) VALUES($1,'google','synthetic-google-sub')`, "23505", passwordUser)
	expectSQLState(`INSERT INTO vault.user_identities(user_id,provider,provider_subject,password_hash) VALUES($1,'google','another-sub',repeat('x',100))`, "23514", passwordUser)
	expectSQLState(`INSERT INTO vault.user_identities(user_id,provider,provider_subject) VALUES($1,'password','person@example.com')`, "23514", passwordUser)
	// There is no email-based linking trigger: provider insertion preserves its exact user.
	var owner string
	if err = tx.QueryRow(ctx, `SELECT user_id::text FROM vault.user_identities WHERE provider='google' AND provider_subject='synthetic-google-sub'`).Scan(&owner); err != nil || owner != googleUser {
		t.Fatal("identity ownership changed")
	}
	// Refuse destructive rollback once identities or verified emails exist. Remove
	// transaction wrappers because this test already owns a transaction/savepoint.
	down := migrationSQL(t, "000016_account_identities.down.sql")
	expectSQLState(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(down), "BEGIN;"), "COMMIT;"), "P0001")
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if before != snapshot() {
		t.Fatal("identity tests changed legacy accounts")
	}
}
