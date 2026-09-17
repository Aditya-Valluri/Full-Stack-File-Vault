//go:build integration

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"balkanid.local/vault/api/internal/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Reuses the provisioning fixture's isolated database, never local account data.
func testSessionLifecycle(t *testing.T, ctx context.Context, admin *pgx.Conn, dsn string) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("pool config failed")
	}
	cfg.ConnConfig.RuntimeParams["application_name"] = "session-test-replica"
	a, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("pool failed")
	}
	defer a.Close()
	b, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal("pool failed")
	}
	defer b.Close()
	first, err := auth.NewSessionStore(a, 24*time.Hour, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	second, err := auth.NewSessionStore(b, 24*time.Hour, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var user string
	if err = admin.QueryRow(ctx, "SELECT user_id::text FROM vault.credentials WHERE login_name='reviewer.one'").Scan(&user); err != nil {
		t.Fatal("test user missing")
	}
	create := func() string {
		token, state, err := first.Create(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		if state.UserID != user || state.Role != "USER" || len(state.CSRFToken) != 43 || state.CSRFToken == token {
			t.Fatal("bad session metadata")
		}
		return token
	}
	invalid := func(token string) {
		t.Helper()
		if _, err := second.LookupAndTouch(ctx, token); !errors.Is(err, auth.ErrInvalidSession) {
			t.Fatalf("expected invalid session, got %v", err)
		}
	}
	token := create()
	state, err := second.LookupAndTouch(ctx, token)
	if err != nil || state.UserID != user {
		t.Fatal("cross-replica lookup failed")
	}
	var stored bool
	if err = admin.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM vault.sessions WHERE encode(token_hash,'hex')=$1 OR csrf_token=$1)", token).Scan(&stored); err != nil || stored {
		t.Fatal("bearer token stored or query failed")
	}
	if err = first.Revoke(ctx, token); err != nil {
		t.Fatal(err)
	}
	invalid(token)
	if err = second.Revoke(ctx, token); err != nil {
		t.Fatal("revoke not idempotent")
	}
	// Control expiry through SQL instead of sleeping; all checks use DB time.
	for _, expiry := range []string{"idle", "absolute"} {
		token = create()
		sql := `UPDATE vault.sessions SET created_at=clock_timestamp()-interval '2 days',last_seen_at=clock_timestamp()-interval '1 day',idle_expires_at=clock_timestamp()-interval '1 hour' WHERE user_id=$1 AND revoked_at IS NULL`
		if expiry == "absolute" {
			sql = `UPDATE vault.sessions SET created_at=clock_timestamp()-interval '3 days',last_seen_at=clock_timestamp()-interval '2 days',idle_expires_at=clock_timestamp()-interval '1 day',expires_at=clock_timestamp()-interval '1 day' WHERE user_id=$1 AND revoked_at IS NULL`
		}
		if _, err = admin.Exec(ctx, sql, user); err != nil {
			t.Fatal("expiry setup failed")
		}
		invalid(token)
	}
	token = create()
	if _, err = admin.Exec(ctx, "UPDATE vault.users SET disabled_at=clock_timestamp() WHERE id=$1", user); err != nil {
		t.Fatal("disable setup failed")
	}
	invalid(token)
	if _, _, err = first.Create(ctx, user); !errors.Is(err, auth.ErrInvalidSession) {
		t.Fatal("disabled user created session")
	}
	if _, err = admin.Exec(ctx, "UPDATE vault.users SET disabled_at=NULL WHERE id=$1", user); err != nil {
		t.Fatal("enable setup failed")
	}
	token = create()
	another := create()
	if err = first.RevokeUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	invalid(token)
	invalid(another)

	// Hold an uncommitted revocation row lock, observe replica B waiting for it,
	// then commit. Lookup must recheck the revoked state rather than resurrect it.
	token = create()
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal("race transaction failed")
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, "UPDATE vault.sessions SET revoked_at=clock_timestamp() WHERE user_id=$1 AND revoked_at IS NULL", user); err != nil {
		t.Fatal("race setup failed")
	}
	result := make(chan error, 1)
	go func() { _, err := second.LookupAndTouch(ctx, token); result <- err }()
	blocked := false
	for i := 0; i < 60; i++ {
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name='session-test-replica' AND cardinality(pg_blocking_pids(pid))>0)`).Scan(&blocked); err != nil {
			t.Fatal("lock observation failed")
		}
		if blocked {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("concurrent lookup did not wait on revocation")
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal("race commit failed")
	}
	select {
	case err := <-result:
		if !errors.Is(err, auth.ErrInvalidSession) {
			t.Fatalf("revocation lost: %v", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("lookup did not complete")
	}
	invalid(token)
	token = create()
	var forbidden bool
	if err = admin.QueryRow(ctx, `SELECT has_column_privilege('vault_runtime','vault.sessions','token_hash','UPDATE') OR has_column_privilege('vault_runtime','vault.sessions','user_id','UPDATE') OR has_table_privilege('vault_runtime','vault.sessions','DELETE')`).Scan(&forbidden); err != nil || forbidden {
		t.Fatal("excess runtime session grants")
	}
	b.Close()
	if _, err = second.LookupAndTouch(ctx, token); !errors.Is(err, auth.ErrSessionStore) {
		t.Fatal("unavailable store did not fail closed")
	}
	t.Log("cross-replica lookup, expiry, disabled user, revocation race, privileges and unavailable-store checks passed")
}
