//go:build integration

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"full-stack-file-vault.local/api/internal/auth"
	"full-stack-file-vault.local/api/internal/graph"
	"full-stack-file-vault.local/api/internal/server"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testGraphQLLogin(t *testing.T, ctx context.Context, admin *pgx.Conn, dsn, password string) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store, err := auth.NewSessionStore(pool, 24*time.Hour, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	boundary, err := auth.NewBrowserSecurity(auth.BrowserConfig{Origin: "https://vault.example.com"}, store)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	h := graph.NewAuthenticationHandler(logger, boundary, func(ctx context.Context, token string) (string, auth.Session, bool, error) {
		return store.BeginSession(ctx, token, 60)
	}, store.LoginBrowser, store.Revoke)
	srv := server.New("", func(context.Context) error { return nil }, logger, h)
	httpServer := httptest.NewTLSServer(srv.Handler)
	defer httpServer.Close()
	request := func(query, name, secret, csrf string, cookie *http.Cookie) (int, string, []*http.Cookie) {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"query": query, "variables": map[string]any{"input": map[string]string{"loginName": name, "password": secret}}})
		req, err := http.NewRequestWithContext(ctx, "POST", httpServer.URL+"/graphql", bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://vault.example.com")
		req.Header.Set("X-CSRF-Token", csrf)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		resp, err := httpServer.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("missing no-store")
		}
		if secret != "" && strings.Contains(string(body), secret) {
			t.Fatal("password exposed in response")
		}
		return resp.StatusCode, string(body), resp.Cookies()
	}
	reset := func() {
		t.Helper()
		if _, err := admin.Exec(ctx, `TRUNCATE vault.login_attempts;
 UPDATE vault.login_budget SET window_start=date_trunc('minute',clock_timestamp())+interval '1 hour',attempts=0`); err != nil {
			t.Fatal(err)
		}
	}
	reset()
	const query = `mutation SignIn($input:LoginInput!){login(input:$input){csrfToken user{id role}}}`
	anon, state, err := store.CreateAnonymous(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: "__Host-vault_session", Value: anon}
	status, body, cookies := request(query, " REVIEWER.ONE ", password, state.CSRFToken, cookie)
	if status != 200 || len(cookies) != 1 || strings.Contains(body, `"errors"`) {
		t.Fatalf("login failed: %d %s", status, body)
	}
	authenticatedCookie := cookies[0]
	authenticated, err := store.LookupAndTouch(ctx, authenticatedCookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	if authenticatedCookie.Value == anon || authenticated.CSRFToken == state.CSRFToken || authenticated.UserID == "" || authenticated.Role != "USER" {
		t.Fatal("identity or rotation failed")
	}
	if !authenticatedCookie.Secure || !authenticatedCookie.HttpOnly || authenticatedCookie.SameSite != http.SameSiteLaxMode || authenticatedCookie.Path != "/" || authenticatedCookie.Domain != "" {
		t.Fatal("unsafe authenticated cookie")
	}
	if !strings.Contains(body, authenticated.UserID) || !strings.Contains(body, authenticated.CSRFToken) || strings.Contains(body, authenticatedCookie.Value) {
		t.Fatal("incorrect login payload")
	}
	if _, err = store.LookupAnonymous(ctx, anon); !errors.Is(err, auth.ErrInvalidSession) {
		t.Fatal("old anonymous session still valid")
	}
	status, _, _ = request(query, "reviewer.one", password, state.CSRFToken, cookie)
	if status != 401 {
		t.Fatal("consumed anonymous cookie accepted")
	}
	status, body, cookies = request(query, "reviewer.one", password, authenticated.CSRFToken, authenticatedCookie)
	if status != 200 || !strings.Contains(body, "FORBIDDEN") || len(cookies) != 0 {
		t.Fatal("authenticated session allowed another login")
	}

	reset()
	anon, state, err = store.CreateAnonymous(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cookie.Value = anon
	var rejected string
	for _, name := range []string{"reviewer.one", "missing.user"} {
		status, body, cookies = request(query, name, "incorrect-private-password", state.CSRFToken, cookie)
		if status != 200 || !strings.Contains(body, "UNAUTHENTICATED") || len(cookies) != 0 {
			t.Fatal("invalid credential response")
		}
		if rejected != "" && body != rejected {
			t.Fatal("credential errors permit enumeration")
		}
		rejected = body
	}
	if _, err = admin.Exec(ctx, "UPDATE vault.users SET disabled_at=clock_timestamp() WHERE id=$1", authenticated.UserID); err != nil {
		t.Fatal(err)
	}
	status, body, cookies = request(query, "reviewer.one", password, state.CSRFToken, cookie)
	if status != 200 || body != rejected || len(cookies) != 0 {
		t.Fatal("disabled user response differs")
	}
	if _, err = admin.Exec(ctx, "UPDATE vault.users SET disabled_at=NULL WHERE id=$1", authenticated.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.LookupAnonymous(ctx, anon); err != nil {
		t.Fatal("rejected credentials consumed anonymous state")
	}
	status, _, cookies = request(query, "reviewer.one", password, strings.Repeat("x", 43), cookie)
	if status != 403 || len(cookies) != 0 {
		t.Fatal("wrong CSRF accepted")
	}

	reset()
	// Concurrent replicas share the identifier cap even across different peer IPs.
	otherPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer otherPool.Close()
	other, err := auth.NewSessionStore(otherPool, 24*time.Hour, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := store
			if i%2 == 1 {
				s = other
			}
			_, _, e := s.LoginBrowser(ctx, anon, state.CSRFToken, fmt.Sprintf("192.0.2.%d", i+1), " REVIEWER.ONE ", []byte("wrong-password"))
			results <- e
		}(i)
	}
	wg.Wait()
	close(results)
	admitted, limited := 0, 0
	for e := range results {
		if errors.Is(e, auth.ErrLoginRejected) {
			admitted++
		} else if errors.Is(e, auth.ErrLoginLimited) {
			limited++
		} else {
			t.Fatal(e)
		}
	}
	if admitted != 5 || limited != 3 {
		t.Fatalf("identifier budget: admitted=%d limited=%d", admitted, limited)
	}
	status, body, cookies = request(query, "reviewer.one", password, state.CSRFToken, cookie)
	if status != 200 || !strings.Contains(body, "RATE_LIMITED") || len(cookies) != 0 {
		t.Fatal("identifier budget bypassed")
	}
	// Expiry permits attempts again, without resetting the global guard.
	if _, err = admin.Exec(ctx, "UPDATE vault.login_attempts SET expires_at=clock_timestamp()-interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	status, body, cookies = request(query, "reviewer.one", password, state.CSRFToken, cookie)
	if status != 200 || len(cookies) != 1 || strings.Contains(body, `"errors"`) {
		t.Fatal("expired throttle did not recover")
	}

	reset()
	peerHash := sha256.Sum256([]byte("192.0.2.50"))
	if _, err = admin.Exec(ctx, "INSERT INTO vault.login_attempts VALUES('peer',$1,clock_timestamp()+interval '1 hour',20)", peerHash[:]); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.LoginBrowser(ctx, "invalid", "invalid", "192.0.2.50", "new.identifier", nil); !errors.Is(err, auth.ErrLoginLimited) {
		t.Fatal("peer cap bypassed")
	}
	var rows int
	if err = admin.QueryRow(ctx, "SELECT count(*) FROM vault.login_attempts").Scan(&rows); err != nil || rows != 1 {
		t.Fatal("peer denial allocated an identifier row")
	}
	if _, err = admin.Exec(ctx, "UPDATE vault.login_budget SET attempts=60"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.LoginBrowser(ctx, "invalid", "invalid", "192.0.2.51", "another.identifier", nil); !errors.Is(err, auth.ErrLoginLimited) {
		t.Fatal("global cap bypassed")
	}
	if err = admin.QueryRow(ctx, "SELECT count(*) FROM vault.login_attempts").Scan(&rows); err != nil || rows != 1 {
		t.Fatal("global denial allocated rows")
	}
	if _, err = admin.Exec(ctx, "UPDATE vault.login_budget SET window_start=clock_timestamp()-interval '2 minutes'"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.LoginBrowser(ctx, "invalid", "invalid", "192.0.2.51", "another.identifier", nil); !errors.Is(err, auth.ErrLoginRejected) {
		t.Fatal("global window did not recover")
	}

	reset()
	anon, state, err = store.CreateAnonymous(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cookie.Value = anon
	// Insert failure rolls rotation back but retains the independently committed attempt.
	if _, err = admin.Exec(ctx, `CREATE FUNCTION vault.reject_login_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private injected database failure'; END $$;
 CREATE TRIGGER reject_login_test BEFORE INSERT ON vault.sessions FOR EACH ROW EXECUTE FUNCTION vault.reject_login_test()`); err != nil {
		t.Fatal(err)
	}
	status, body, cookies = request(query, "reviewer.one", password, state.CSRFToken, cookie)
	if status != 200 || !strings.Contains(body, "INTERNAL_ERROR") || len(cookies) != 0 || strings.Contains(body, "injected") {
		t.Fatal("unsafe login transaction failure")
	}
	if _, err = store.LookupAnonymous(ctx, anon); err != nil {
		t.Fatal("failed insert consumed anonymous session")
	}
	var attempts int
	if err = admin.QueryRow(ctx, "SELECT attempts FROM vault.login_budget").Scan(&attempts); err != nil || attempts != 1 {
		t.Fatal("failed login did not retain throttle charge")
	}
	if _, err = admin.Exec(ctx, `DROP TRIGGER reject_login_test ON vault.sessions; DROP FUNCTION vault.reject_login_test()`); err != nil {
		t.Fatal(err)
	}
	pool.Close()
	status, body, cookies = request(query, "reviewer.one", password, state.CSRFToken, cookie)
	if status != 503 || !strings.Contains(body, "INTERNAL_ERROR") || len(cookies) != 0 {
		t.Fatal("database failure did not fail closed")
	}
	if strings.Contains(logs.String(), password) || strings.Contains(logs.String(), "incorrect-private-password") || strings.Contains(logs.String(), "injected") {
		t.Fatal("secrets in logs")
	}
}
