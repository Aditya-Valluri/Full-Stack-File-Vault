//go:build integration

package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"full-stack-file-vault.local/api/internal/auth"
	"full-stack-file-vault.local/api/internal/mailer"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testPasswordRecovery(t *testing.T, ctx context.Context, operator *pgx.Conn, dsn string) {
	for _, suffix := range []string{"up", "down", "up"} {
		if _, err := operator.Exec(ctx, migrationSQL(t, "000019_password_recovery."+suffix+".sql")); err != nil {
			t.Fatal(err)
		}
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store, err := auth.NewSessionStore(pool, 24*time.Hour, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	capture := &capturedRegistrationMail{}
	store.ConfigureMail(capture)
	resetBudget := func() {
		t.Helper()
		if _, err := operator.Exec(ctx, "TRUNCATE vault.login_attempts; UPDATE vault.login_budget SET attempts=0,window_start=date_trunc('minute',clock_timestamp())"); err != nil {
			t.Fatal(err)
		}
	}
	anon := func() (string, auth.Session) {
		t.Helper()
		raw, state, err := store.CreateAnonymous(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return raw, state
	}
	login := func(name, password string) (string, auth.Session, error) {
		raw, state := anon()
		return store.Login(ctx, raw, state.CSRFToken, name, []byte(password))
	}
	request := func(raw string, state auth.Session, email string) string {
		t.Helper()
		resetBudget()
		before := len(capture.messages)
		if err := store.RequestPasswordReset(ctx, raw, state.CSRFToken, "127.0.0.1", email); err != nil {
			t.Fatal(err)
		}
		if len(capture.messages) != before+1 || capture.messages[before].Purpose != mailer.ResetPassword || len(capture.messages[before].Code) != 7 {
			t.Fatal("reset email count or format")
		}
		return capture.messages[before].Code
	}
	email := "new.owner@example.com"
	original := "a long registration password"
	changed := "orchard comet velvet pavilion"
	finalPassword := "violet harbor lantern meadow"
	first, firstState, err := login(email, original)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := login(email, original)
	if err != nil {
		t.Fatal(err)
	}
	resetBudget()
	if err = store.ChangePassword(ctx, first, "wrong", "127.0.0.1", []byte(original), []byte(changed)); err != auth.ErrUnauthenticated {
		t.Fatal("CSRF bypass")
	}
	if err = store.ChangePassword(ctx, first, firstState.CSRFToken, "127.0.0.1", []byte("wrong"), []byte(changed)); err != auth.ErrPasswordRejected {
		t.Fatal("current password not required")
	}
	if err = store.ChangePassword(ctx, first, firstState.CSRFToken, "127.0.0.1", []byte(original), []byte("Password123456789!")); err != auth.ErrWeakPassword {
		t.Fatal("weak replacement accepted")
	}
	pending, pendingState := anon()
	pendingCode := request(pending, pendingState, email)
	resetBudget()
	if err = store.ChangePassword(ctx, first, firstState.CSRFToken, "127.0.0.1", []byte(original), []byte(changed)); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{first, second} {
		if _, err = store.LookupBrowserSession(ctx, token); !errors.Is(err, auth.ErrInvalidSession) {
			t.Fatal("change left session active")
		}
	}
	if _, _, err = login(email, original); err != auth.ErrLoginRejected {
		t.Fatal("old password survived change")
	}
	resetBudget()
	if err = store.CompletePasswordReset(ctx, pending, pendingState.CSRFToken, "127.0.0.1", pendingCode, []byte(finalPassword)); err != auth.ErrResetRejected {
		t.Fatal("change left reset code active")
	}
	current, currentState, err := login(email, changed)
	if err != nil {
		t.Fatal(err)
	}
	if currentState.UserID != firstState.UserID {
		t.Fatal("account ownership changed")
	}
	pending, pendingState = anon()
	resetBudget()
	for i := 0; i < 5; i++ {
		_, _, failure := store.LoginBrowser(ctx, pending, pendingState.CSRFToken, "127.0.0.1", email, []byte("wrong"))
		if i < 4 && !errors.Is(failure, auth.ErrLoginRejected) {
			t.Fatal("unexpected password restriction")
		}
		if i == 4 && !errors.Is(failure, auth.ErrLoginLimited) {
			t.Fatal("fifth failure not restricted")
		}
	}
	beforeMail := len(capture.messages)
	if err = store.RequestPasswordReset(ctx, pending, pendingState.CSRFToken, "127.0.0.1", email); err != nil {
		t.Fatal("password lockout blocked recovery")
	}
	if len(capture.messages) != beforeMail+1 {
		t.Fatal("recovery did not send exactly one code")
	}
	code := capture.messages[beforeMail].Code
	foreign, foreignState := anon()
	if err = store.CompletePasswordReset(ctx, foreign, foreignState.CSRFToken, "127.0.0.2", code, []byte(finalPassword)); err != auth.ErrResetRejected {
		t.Fatal("reset accepted another browser")
	}
	if err = store.CompletePasswordReset(ctx, pending, "bad", "127.0.0.1", code, []byte(finalPassword)); err != auth.ErrLoginForbidden {
		t.Fatal("reset CSRF bypass")
	}
	if err = store.CompletePasswordReset(ctx, pending, pendingState.CSRFToken, "127.0.0.1", code, []byte("Password123456789!")); err != auth.ErrWeakPassword {
		t.Fatal("weak reset accepted")
	}
	// Keep the password restriction while resetting unrelated request budgets.
	if _, err = operator.Exec(ctx, "UPDATE vault.login_budget SET attempts=0; DELETE FROM vault.login_attempts WHERE scope<>'password'"); err != nil {
		t.Fatal(err)
	}
	// Two valid concurrent redemptions must yield exactly one change.
	var wg sync.WaitGroup
	outcomes := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			outcomes <- store.CompletePasswordReset(ctx, pending, pendingState.CSRFToken, "127.0.0.1", code, []byte(finalPassword))
		}()
	}
	wg.Wait()
	close(outcomes)
	successes := 0
	for outcome := range outcomes {
		if outcome == nil {
			successes++
		} else if outcome != auth.ErrResetRejected && outcome != auth.ErrLoginForbidden {
			t.Fatalf("unexpected concurrent reset failure: %v", outcome)
		}
	}
	if successes != 1 {
		t.Fatal("reset not single-use")
	}
	var restrictions int
	if err = operator.QueryRow(ctx, "SELECT count(*) FROM vault.login_attempts WHERE scope='password'").Scan(&restrictions); err != nil || restrictions != 0 {
		t.Fatal("reset left password restriction")
	}
	fresh, freshState := anon()
	if _, _, err = store.LoginBrowser(ctx, fresh, freshState.CSRFToken, "127.0.0.2", email, []byte(finalPassword)); err != nil {
		t.Fatal("fresh login after recovery failed")
	}
	if _, err = store.LookupBrowserSession(ctx, current); !errors.Is(err, auth.ErrInvalidSession) {
		t.Fatal("reset left session active")
	}
	if _, _, err = login(email, changed); err != auth.ErrLoginRejected {
		t.Fatal("old password survived reset")
	}
	if _, _, err = login(email, finalPassword); err != nil {
		t.Fatal("new reset password not accepted")
	}
	unknown, unknownState := anon()
	unknownCode := request(unknown, unknownState, "unknown-reset@example.com")
	if err = store.CompletePasswordReset(ctx, unknown, unknownState.CSRFToken, "127.0.0.1", unknownCode, []byte(changed)); err != auth.ErrResetRejected {
		t.Fatal("unknown email created/reset account")
	}
	expired, expiredState := anon()
	expiredCode := request(expired, expiredState, email)
	if _, err = operator.Exec(ctx, "UPDATE vault.password_reset_challenges SET expires_at=clock_timestamp()-interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	if err = store.CompletePasswordReset(ctx, expired, expiredState.CSRFToken, "127.0.0.1", expiredCode, []byte(changed)); err != auth.ErrResetRejected {
		t.Fatal("expired reset accepted")
	}
	locked, lockedState := anon()
	lockedCode := request(locked, lockedState, email)
	wrong := "0000000"
	if wrong == lockedCode {
		wrong = "0000001"
	}
	for i := 0; i < 5; i++ {
		resetBudget()
		if err = store.CompletePasswordReset(ctx, locked, lockedState.CSRFToken, "127.0.0.1", wrong, []byte(changed)); err != auth.ErrResetRejected {
			t.Fatal("incorrect reset accepted")
		}
	}
	resetBudget()
	if err = store.CompletePasswordReset(ctx, locked, lockedState.CSRFToken, "127.0.0.1", lockedCode, []byte(changed)); err != auth.ErrResetRejected {
		t.Fatal("attempt limit bypassed")
	}
	disabled, disabledState := anon()
	disabledCode := request(disabled, disabledState, email)
	if _, err = operator.Exec(ctx, "UPDATE vault.users SET disabled_at=clock_timestamp() WHERE id=$1", firstState.UserID); err != nil {
		t.Fatal(err)
	}
	if err = store.CompletePasswordReset(ctx, disabled, disabledState.CSRFToken, "127.0.0.1", disabledCode, []byte(changed)); err != auth.ErrResetRejected {
		t.Fatal("disabled account reset")
	}
	if _, err = operator.Exec(ctx, "UPDATE vault.users SET disabled_at=NULL WHERE id=$1", firstState.UserID); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{"SELECT * FROM vault.password_reset_challenges", "DELETE FROM vault.password_reset_challenges", "UPDATE vault.credentials SET password_hash=password_hash", "UPDATE vault.user_identities SET password_hash=password_hash"} {
		if _, err = pool.Exec(ctx, sql); err == nil {
			t.Fatal("runtime credential/table privilege widened")
		}
	}
	// A separate legacy account exercises password change without a mail sender.
	legacyHash, err := auth.HashPassword([]byte(original))
	if err != nil {
		t.Fatal(err)
	}
	var legacyID string
	if err = operator.QueryRow(ctx, "INSERT INTO vault.users DEFAULT VALUES RETURNING id::text").Scan(&legacyID); err != nil {
		t.Fatal(err)
	}
	if _, err = operator.Exec(ctx, "INSERT INTO vault.credentials(user_id,login_name,password_hash) VALUES($1,'recovery.legacy',$2)", legacyID, legacyHash); err != nil {
		t.Fatal(err)
	}
	legacy, legacyState, err := login("recovery.legacy", original)
	if err != nil {
		t.Fatal(err)
	}
	store.ConfigureMail(nil)
	resetBudget()
	if err = store.ChangePassword(ctx, legacy, legacyState.CSRFToken, "127.0.0.1", []byte(original), []byte(changed)); err != nil {
		t.Fatal(err)
	}
	if _, _, err = login("recovery.legacy", changed); err != nil {
		t.Fatal("legacy password change failed")
	}
	if _, _, err = login("recovery.legacy", original); err != auth.ErrLoginRejected {
		t.Fatal("old legacy password survived")
	}
}
