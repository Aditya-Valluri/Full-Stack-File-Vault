//go:build integration

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"file-vault.local/api/internal/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testLoginRotation(t *testing.T, ctx context.Context, admin *pgx.Conn, dsn, password string) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("pool config")
	}
	cfg.ConnConfig.RuntimeParams["application_name"] = "login-test"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("pool")
	}
	defer pool.Close()
	first, err := auth.NewSessionStore(pool, 24*time.Hour, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	second, err := auth.NewSessionStore(pool, 24*time.Hour, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	create := func() (string, auth.Session) {
		token, s, err := first.CreateAnonymous(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if s.UserID != "" || s.Role != "" {
			t.Fatal("anonymous identity populated")
		}
		return token, s
	}
	login := func(token, csrf string) (string, auth.Session, error) {
		return first.Login(ctx, token, csrf, "reviewer.one", []byte(password))
	}
	anon, state := create()
	if _, err = first.LookupAndTouch(ctx, anon); !errors.Is(err, auth.ErrInvalidSession) {
		t.Fatal("anonymous authenticated")
	}
	for _, tc := range []struct{ name, secret, csrf string }{{"reviewer.one", "wrong-password", state.CSRFToken}, {"missing.user", password, state.CSRFToken}, {"reviewer.one", password, "wrong"}} {
		if _, _, err = first.Login(ctx, anon, tc.csrf, tc.name, []byte(tc.secret)); !errors.Is(err, auth.ErrLoginRejected) {
			t.Fatal("bad credentials/CSRF accepted")
		}
		if _, err = first.LookupAnonymous(ctx, anon); err != nil {
			t.Fatal("failed login consumed session")
		}
	}
	token, authenticated, err := login(anon, state.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	if token == anon || authenticated.CSRFToken == state.CSRFToken || authenticated.UserID == "" {
		t.Fatal("tokens not rotated")
	}
	if _, err = first.LookupAnonymous(ctx, anon); !errors.Is(err, auth.ErrInvalidSession) {
		t.Fatal("consumed session reusable")
	}
	if _, _, err = login(anon, state.CSRFToken); !errors.Is(err, auth.ErrLoginRejected) {
		t.Fatal("replayed login accepted")
	}
	if _, err = second.LookupAndTouch(ctx, token); err != nil {
		t.Fatal("new session invalid")
	}
	// Force the insert to fail AFTER anonymous consumption, proving rollback restores it.
	anon, state = create()
	_, err = admin.Exec(ctx, `CREATE FUNCTION vault.fail_session_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.user_id IS NOT NULL THEN RAISE EXCEPTION 'test insertion failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER fail_session_insert BEFORE INSERT ON vault.sessions FOR EACH ROW EXECUTE FUNCTION vault.fail_session_insert();`)
	if err != nil {
		t.Fatal("failure injection failed")
	}
	if _, _, err = login(anon, state.CSRFToken); !errors.Is(err, auth.ErrSessionStore) {
		t.Fatal("expected insert failure")
	}
	if _, err = first.LookupAnonymous(ctx, anon); err != nil {
		t.Fatal("failed insert consumed anonymous session")
	}
	if _, err = admin.Exec(ctx, "DROP TRIGGER fail_session_insert ON vault.sessions; DROP FUNCTION vault.fail_session_insert();"); err != nil {
		t.Fatal("injection cleanup failed")
	}
	// Both calls start together. Exactly one may consume the same anonymous row.
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, store := range []*auth.SessionStore{first, second} {
		go func(s *auth.SessionStore) {
			<-start
			_, _, err := s.Login(ctx, anon, state.CSRFToken, "reviewer.one", []byte(password))
			results <- err
		}(store)
	}
	close(start)
	wins := 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			wins++
		} else if !errors.Is(err, auth.ErrLoginRejected) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatal("anonymous session consumed more than once")
	}
	// A committed security-version change or account disable invalidates a credential
	// verification that was waiting for the user lock; observe the lock before commit.
	for _, update := range []string{"auth_version=auth_version+1", "disabled_at=clock_timestamp()"} {
		anon, state = create()
		tx, err := admin.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, "UPDATE vault.users SET "+update+" WHERE id=$1", authenticated.UserID); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal("user lock setup")
		}
		done := make(chan error, 1)
		go func(a, c string) { _, _, err := login(a, c); done <- err }(anon, state.CSRFToken)
		blocked := false
		for i := 0; i < 150; i++ {
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name='login-test' AND cardinality(pg_blocking_pids(pid))>0)`).Scan(&blocked); err != nil {
				_ = tx.Rollback(ctx)
				t.Fatal("lock inspection")
			}
			if blocked {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if !blocked {
			_ = tx.Rollback(ctx)
			t.Fatal("login did not reach user lock")
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal("user change commit")
		}
		if err = <-done; !errors.Is(err, auth.ErrLoginRejected) {
			t.Fatal("stale login succeeded")
		}
		if _, err = first.LookupAnonymous(ctx, anon); err != nil {
			t.Fatal("rejected login consumed anonymous state")
		}
	}
	if _, err = admin.Exec(ctx, "UPDATE vault.users SET disabled_at=NULL WHERE id=$1", authenticated.UserID); err != nil {
		t.Fatal("restore test user")
	}
	// Expired anonymous state cannot be used to log in.
	anon, state = create()
	if _, err = admin.Exec(ctx, `UPDATE vault.sessions SET created_at=clock_timestamp()-interval '20 minutes',last_seen_at=clock_timestamp()-interval '20 minutes',idle_expires_at=clock_timestamp()-interval '15 minutes',expires_at=clock_timestamp()-interval '15 minutes' WHERE user_id IS NULL AND revoked_at IS NULL`); err != nil {
		t.Fatal("expiry setup")
	}
	if _, _, err = login(anon, state.CSRFToken); !errors.Is(err, auth.ErrLoginRejected) {
		t.Fatal("expired anonymous session accepted")
	}
	t.Log("failed login, replay, insert rollback, competing rotation, expiry and user security-change races passed")
}
