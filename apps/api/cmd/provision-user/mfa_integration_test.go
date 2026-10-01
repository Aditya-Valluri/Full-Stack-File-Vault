//go:build integration

package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"full-stack-file-vault.local/api/internal/auth"
	"github.com/jackc/pgx/v5"
)

func testMFA(t *testing.T, ctx context.Context, db *pgx.Conn, dsn, directory string) {
	f := newPublicationFixture(t, ctx, db, dsn, directory)
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatal(err)
	}
	if err := f.sessions.ConfigureMFA(base64.StdEncoding.EncodeToString(key[:])); err != nil {
		t.Fatal(err)
	}
	capture := &capturedRegistrationMail{}
	f.sessions.ConfigureMail(capture)
	_, user, cookie, state := f.user(1000)
	const email = "mfa.integration@example.test"
	const password = "violet-copper-planet-lantern-7054"
	const nextPassword = "meadow-river-cobalt-window-8196"
	hash, err := auth.HashPassword([]byte(password))
	if err != nil {
		t.Fatal(err)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := db.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec("UPDATE vault.users SET email_address=$2,email_normalized=$2,email_verified_at=clock_timestamp() WHERE id=$1", user, email)
	exec("INSERT INTO vault.user_identities(user_id,provider,provider_subject,password_hash) VALUES($1,'password',$2,$3)", user, email, hash)
	resetBudget := func() {
		exec("TRUNCATE vault.login_attempts; UPDATE vault.login_budget SET attempts=0,window_start=date_trunc('minute',clock_timestamp())")
	}
	resetBudget()
	bound := func(raw string, session auth.Session) context.Context {
		t.Helper()
		request := httptest.NewRequest("POST", "https://vault.example.com/graphql", nil).WithContext(ctx)
		request.Header.Set("Origin", "https://vault.example.com")
		request.Header.Set("X-CSRF-Token", session.CSRFToken)
		request.AddCookie(&http.Cookie{Name: "__Host-vault_session", Value: raw})
		var result context.Context
		f.browser.Wrap(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { result = r.Context() })).ServeHTTP(httptest.NewRecorder(), request)
		if result == nil {
			t.Fatal("authenticated context unavailable")
		}
		return result
	}
	accountCtx := bound(cookie, state)
	if _, err = f.sessions.MFA(accountCtx, "127.0.0.1", "setup", []byte("wrong"), ""); !errors.Is(err, auth.ErrPasswordRejected) {
		t.Fatal("setup skipped reauthentication")
	}
	setup, err := f.sessions.MFA(accountCtx, "127.0.0.1", "setup", []byte(password), "")
	if err != nil {
		t.Fatal(err)
	}
	uri, err := url.Parse(setup.URI)
	if err != nil {
		t.Fatal("invalid provisioning URI")
	}
	seed, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(uri.Query().Get("secret"))
	if err != nil {
		t.Fatal("invalid seed encoding")
	}
	defer clear(seed)
	codeAt := func(step int64) string {
		var counter [8]byte
		binary.BigEndian.PutUint64(counter[:], uint64(step))
		mac := hmac.New(sha1.New, seed)
		_, _ = mac.Write(counter[:])
		sum := mac.Sum(nil)
		offset := sum[len(sum)-1] & 15
		return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(sum[offset:offset+4])&0x7fffffff)%1000000)
	}
	exec("UPDATE vault.user_mfa SET setup_expires_at=clock_timestamp()-interval '1 second' WHERE user_id=$1", user)
	if _, err = f.sessions.MFA(accountCtx, "127.0.0.1", "enable", []byte(password), codeAt(time.Now().Unix()/30-1)); !errors.Is(err, auth.ErrMFARejected) {
		t.Fatal("expired enrollment accepted")
	}
	exec("UPDATE vault.user_mfa SET setup_expires_at=clock_timestamp()+interval '10 minutes' WHERE user_id=$1", user)
	enabled, err := f.sessions.MFA(accountCtx, "127.0.0.1", "enable", []byte(password), codeAt(time.Now().Unix()/30-1))
	if err != nil || len(enabled.RecoveryCodes) != 8 {
		t.Fatal("enable failed", err)
	}
	if _, err = f.sessions.LookupBrowserSession(ctx, cookie); !errors.Is(err, auth.ErrInvalidSession) {
		t.Fatal("enrollment left session active")
	}
	var encryptedLength, recoveries int
	if err = db.QueryRow(ctx, "SELECT octet_length(encrypted_secret),(SELECT count(*) FROM vault.mfa_recovery_codes WHERE user_id=$1) FROM vault.user_mfa WHERE user_id=$1 AND enabled", user).Scan(&encryptedLength, &recoveries); err != nil || encryptedLength != 49 || recoveries != 8 {
		t.Fatal("at-rest MFA invariant")
	}
	login := func(password, code string) (string, auth.Session, error) {
		raw, anonymous, e := f.sessions.CreateAnonymous(ctx)
		if e != nil {
			return "", auth.Session{}, e
		}
		return f.sessions.Login(auth.WithSecondFactor(ctx, code), raw, anonymous.CSRFToken, email, []byte(password))
	}
	if _, _, err = login(password, ""); !errors.Is(err, auth.ErrMFARequired) {
		t.Fatal("password-only bypass")
	}
	if _, _, err = login(password, "invalid"); !errors.Is(err, auth.ErrLoginRejected) {
		t.Fatal("invalid factor accepted")
	}
	current := codeAt(time.Now().Unix() / 30)
	if _, _, err = login(password, current); err != nil {
		t.Fatal("valid factor rejected", err)
	}
	if _, _, err = login(password, current); !errors.Is(err, auth.ErrLoginRejected) {
		t.Fatal("TOTP replay accepted")
	}
	var wg sync.WaitGroup
	outcomes := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _, e := login(password, enabled.RecoveryCodes[0]); outcomes <- e }()
	}
	wg.Wait()
	close(outcomes)
	successes := 0
	for e := range outcomes {
		if e == nil {
			successes++
		} else if !errors.Is(e, auth.ErrLoginRejected) {
			t.Fatal("unexpected recovery failure")
		}
	}
	if successes != 1 {
		t.Fatal("recovery code was not one-use")
	}
	// A password reset preserves MFA and cannot create an authenticated session.
	resetBudget()
	raw, anonymous, err := f.sessions.CreateAnonymous(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.sessions.RequestPasswordReset(ctx, raw, anonymous.CSRFToken, "127.0.0.1", email); err != nil {
		t.Fatal(err)
	}
	resetCode := capture.messages[len(capture.messages)-1].Code
	if err = f.sessions.CompletePasswordReset(ctx, raw, anonymous.CSRFToken, "127.0.0.1", resetCode, []byte(nextPassword)); err != nil {
		t.Fatal(err)
	}
	if _, _, err = login(nextPassword, ""); !errors.Is(err, auth.ErrMFARequired) {
		t.Fatal("password reset bypassed MFA")
	}
	if _, _, err = login(password, enabled.RecoveryCodes[1]); !errors.Is(err, auth.ErrLoginRejected) {
		t.Fatal("old password survived")
	}
	authenticated, session, err := login(nextPassword, enabled.RecoveryCodes[1])
	if err != nil {
		t.Fatal(err)
	}
	resetBudget()
	if _, err = f.sessions.MFA(bound(authenticated, session), "127.0.0.1", "disable", []byte(nextPassword), enabled.RecoveryCodes[2]); err != nil {
		t.Fatal(err)
	}
	if _, err = f.sessions.LookupBrowserSession(ctx, authenticated); !errors.Is(err, auth.ErrInvalidSession) {
		t.Fatal("disable left session active")
	}
	if _, _, err = login(nextPassword, ""); err != nil {
		t.Fatal("disabled MFA blocks password login", err)
	}
}
