//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"full-stack-file-vault.local/api/internal/auth"
	"full-stack-file-vault.local/api/internal/graph"
	"full-stack-file-vault.local/api/internal/mailer"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type capturedRegistrationMail struct{ messages []mailer.Message }

func (m *capturedRegistrationMail) Send(_ context.Context, message mailer.Message) error {
	m.messages = append(m.messages, message)
	return nil
}

func testEmailRegistration(t *testing.T, ctx context.Context, operator *pgx.Conn, dsn string) {
	for _, suffix := range []string{"up", "down", "up"} {
		if _, err := operator.Exec(ctx, migrationSQL(t, "000017_email_registration."+suffix+".sql")); err != nil {
			t.Fatal(err)
		}
	}
	for _, suffix := range []string{"up", "down", "up"} {
		if _, err := operator.Exec(ctx, migrationSQL(t, "000018_bound_email_codes."+suffix+".sql")); err != nil {
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
	browser, err := auth.NewBrowserSecurity(auth.BrowserConfig{Origin: "https://vault.example.com"}, store)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	handler := graph.NewAuthenticationHandler(slog.New(slog.NewTextHandler(&logs, nil)), browser, func(ctx context.Context, token string) (string, auth.Session, bool, error) {
		return store.BeginSession(ctx, token, 60)
	}, store.LoginBrowser, store.Revoke)
	reset := func() {
		t.Helper()
		if _, err := operator.Exec(ctx, "TRUNCATE vault.login_attempts; UPDATE vault.login_budget SET attempts=0,window_start=date_trunc('minute',clock_timestamp())"); err != nil {
			t.Fatal(err)
		}
	}
	reset()
	anonymous, state, err := store.CreateAnonymous(ctx)
	if err != nil {
		t.Fatal(err)
	}
	call := func(query string, variables map[string]any, token, csrf string) *httptest.ResponseRecorder {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"query": query, "variables": variables})
		request := httptest.NewRequest(http.MethodPost, "https://vault.example.com/graphql", bytes.NewReader(payload))
		request.RemoteAddr = "127.0.0.1:12345"
		request.Header.Set("Origin", "https://vault.example.com")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-CSRF-Token", csrf)
		request.AddCookie(&http.Cookie{Name: "__Host-vault_session", Value: token})
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	requestQuery := "mutation($email:String!){requestEmailRegistration(email:$email)}"
	completeQuery := "mutation($code:String!,$password:String!){completeEmailRegistration(code:$code,password:$password){csrfToken user{id role loginName}}}"
	email := "New.Owner@example.com"
	password := "a long registration password"
	response := call(requestQuery, map[string]any{"email": email}, anonymous, "wrong")
	if response.Code != 403 || len(capture.messages) != 0 {
		t.Fatal("CSRF allowed email delivery")
	}
	response = call(requestQuery, map[string]any{"email": email}, anonymous, state.CSRFToken)
	if !strings.Contains(response.Body.String(), "\"requestEmailRegistration\":true") || len(capture.messages) != 1 {
		t.Fatalf("registration request: %s", response.Body)
	}
	newAccountResponse := response.Body.String()
	code := capture.messages[0].Code
	if len(code) != 7 || strings.Trim(code, "0123456789") != "" {
		t.Fatal("not seven digits")
	}
	other, otherState, err := store.CreateAnonymous(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.CompleteEmailRegistration(ctx, other, otherState.CSRFToken, "127.0.0.2", code, []byte(password)); !errors.Is(err, auth.ErrEmailVerification) {
		t.Fatal("code accepted in another browser")
	}
	if strings.Contains(response.Body.String(), code) {
		t.Fatal("code exposed")
	}
	var count int
	if err = operator.QueryRow(ctx, "SELECT count(*) FROM vault.users WHERE email_normalized='new.owner@example.com'").Scan(&count); err != nil || count != 0 {
		t.Fatal("account created before verification")
	}
	response = call(completeQuery, map[string]any{"code": code, "password": "short"}, anonymous, state.CSRFToken)
	if !strings.Contains(response.Body.String(), "WEAK_PASSWORD") {
		t.Fatalf("weak password accepted: %s", response.Body)
	}
	response = call(completeQuery, map[string]any{"code": code, "password": "Password123456789!"}, anonymous, state.CSRFToken)
	if !strings.Contains(response.Body.String(), "WEAK_PASSWORD") {
		t.Fatal("common password accepted")
	}
	response = call(completeQuery, map[string]any{"code": code, "password": password}, anonymous, state.CSRFToken)
	if strings.Contains(response.Body.String(), "errors") || len(response.Result().Cookies()) != 1 {
		t.Fatalf("verification: %s", response.Body)
	}
	cookie := response.Result().Cookies()[0]
	if !cookie.Secure || !cookie.HttpOnly || cookie.Value == anonymous {
		t.Fatal("session not securely rotated")
	}
	authenticated, err := store.LookupBrowserSession(ctx, cookie.Value)
	if err != nil || authenticated.LoginName != email || authenticated.Role != "USER" {
		t.Fatalf("identity: %+v %v", authenticated, err)
	}
	var quota int64
	if err = operator.QueryRow(ctx, "SELECT quota_bytes FROM vault.users WHERE id=$1", authenticated.UserID).Scan(&quota); err != nil || quota != 10000000 {
		t.Fatal("registration changed quota")
	}
	if _, err = store.LookupAnonymous(ctx, anonymous); !errors.Is(err, auth.ErrInvalidSession) {
		t.Fatal("anonymous session survived")
	}
	reset()
	anonymous, state, err = store.CreateAnonymous(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.CompleteEmailRegistration(ctx, anonymous, state.CSRFToken, "127.0.0.1", code, []byte(password)); !errors.Is(err, auth.ErrEmailVerification) {
		t.Fatal("verification code reused")
	}
	// Email case folding, legacy credentials and wrong/unknown generic responses.
	raw, logged, err := store.LoginBrowser(ctx, anonymous, state.CSRFToken, "127.0.0.1", "NEW.OWNER@EXAMPLE.COM", []byte(password))
	if err != nil || logged.UserID != authenticated.UserID {
		t.Fatalf("email login failed: %v", err)
	}
	if err = store.Revoke(ctx, raw); err != nil {
		t.Fatal(err)
	}
	if _, err = store.LookupBrowserSession(ctx, raw); !errors.Is(err, auth.ErrInvalidSession) {
		t.Fatal("logout did not revoke")
	}
	reset()
	anonymous, state, err = store.CreateAnonymous(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, identifier := range []string{email, "unknown@example.com"} {
		if _, _, err = store.LoginBrowser(ctx, anonymous, state.CSRFToken, "127.0.0.1", identifier, []byte("wrong")); !errors.Is(err, auth.ErrLoginRejected) {
			t.Fatalf("different failure for email: %v", err)
		}
	}
	reset()
	response = call(requestQuery, map[string]any{"email": email}, anonymous, state.CSRFToken)
	if !strings.Contains(response.Body.String(), "\"requestEmailRegistration\":true") {
		t.Fatal("duplicate disclosed")
	}
	if response.Body.String() != newAccountResponse {
		t.Fatal("registration response exposes account existence")
	}
	existingMail := capture.messages[len(capture.messages)-1]
	if existingMail.Purpose != mailer.ExistingAccount || existingMail.Code != "" {
		t.Fatal("existing account received registration code")
	}
	if err = operator.QueryRow(ctx, "SELECT count(*) FROM vault.email_challenges WHERE email_normalized='new.owner@example.com'").Scan(&count); err != nil || count != 0 {
		t.Fatal("existing account has registration challenge")
	}
	duplicateCode := "0000000"
	response = call(completeQuery, map[string]any{"code": duplicateCode, "password": password}, anonymous, state.CSRFToken)
	if !strings.Contains(response.Body.String(), "REGISTRATION_REJECTED") {
		t.Fatal("duplicate account merged")
	}
	if err = operator.QueryRow(ctx, "SELECT count(*) FROM vault.users WHERE email_normalized='new.owner@example.com'").Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate account created")
	}
	if _, err = operator.Exec(ctx, "UPDATE vault.email_challenges SET expires_at=clock_timestamp()-interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.CompleteEmailRegistration(ctx, anonymous, state.CSRFToken, "127.0.0.1", duplicateCode, []byte(password)); !errors.Is(err, auth.ErrEmailVerification) {
		t.Fatal("expired code accepted")
	}
	reset()
	if err = store.RequestEmailRegistration(ctx, anonymous, state.CSRFToken, "127.0.0.1", "locked@example.com"); err != nil {
		t.Fatal(err)
	}
	lockedCode := capture.messages[len(capture.messages)-1].Code
	wrongCode := "0000000"
	if lockedCode == wrongCode {
		wrongCode = "0000001"
	}
	for i := 0; i < 5; i++ {
		reset() // Isolate the persistent challenge counter from peer throttling.
		if _, _, err = store.CompleteEmailRegistration(ctx, anonymous, state.CSRFToken, "127.0.0.1", wrongCode, []byte(password)); !errors.Is(err, auth.ErrEmailVerification) {
			t.Fatal("wrong code accepted")
		}
	}
	reset()
	if _, _, err = store.CompleteEmailRegistration(ctx, anonymous, state.CSRFToken, "127.0.0.1", lockedCode, []byte(password)); !errors.Is(err, auth.ErrEmailVerification) {
		t.Fatal("exhausted code accepted")
	}
	if _, err = pool.Exec(ctx, "UPDATE vault.email_challenges SET attempts=0"); err == nil {
		t.Fatal("runtime can reset attempts")
	}
	store.ConfigureMail(nil)
	if err = store.RequestEmailRegistration(ctx, anonymous, state.CSRFToken, "127.0.0.1", email); !errors.Is(err, auth.ErrEmailDisabled) {
		t.Fatal("unconfigured registration enabled")
	}
	for _, secret := range []string{password, code, duplicateCode, email, "$argon2id"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("secret in logs")
		}
	}
	// Runtime still cannot bypass the narrowly scoped creation function.
	if _, err = pool.Exec(ctx, "INSERT INTO vault.users DEFAULT VALUES"); err == nil {
		t.Fatal("runtime gained arbitrary account creation")
	}
}
