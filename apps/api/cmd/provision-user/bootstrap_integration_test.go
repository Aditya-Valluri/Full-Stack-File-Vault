//go:build integration

package main

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testBootstrap(t *testing.T, ctx context.Context, admin *pgx.Conn, dsn string) {
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
	handler := graph.NewBrowserHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), boundary, func(ctx context.Context, token string) (string, auth.Session, bool, error) {
		return store.BeginSession(ctx, token, 60)
	})
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	request := func(query, header, origin string, cookie *http.Cookie) (int, string, []*http.Cookie) {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"query": query})
		req, err := http.NewRequestWithContext(ctx, "POST", server.URL, strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		if header != "" {
			req.Header.Set("X-Vault-CSRF-Bootstrap", header)
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		payload, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("missing no-store")
		}
		return resp.StatusCode, string(payload), resp.Cookies()
	}
	const mutation = `mutation { beginSession { csrfToken } }`
	const origin = "https://vault.example.com"
	status, body, cookies := request(mutation, "1", origin, nil)
	if status != 200 || len(cookies) != 1 || strings.Contains(body, `"errors"`) {
		t.Fatalf("bootstrap failed: %d %s", status, body)
	}
	cookie := cookies[0]
	if cookie.Name != "__Host-vault_session" || !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/" || cookie.Domain != "" || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatal("unsafe cookie")
	}
	state, err := store.LookupAnonymous(ctx, cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, state.CSRFToken) || strings.Contains(body, cookie.Value) {
		t.Fatal("incorrect response credentials")
	}
	status, body, cookies = request(mutation, "1", origin, cookie)
	if status != 200 || len(cookies) != 0 || !strings.Contains(body, state.CSRFToken) {
		t.Fatal("anonymous session not reused")
	}
	var user string
	if err = admin.QueryRow(ctx, "SELECT user_id::text FROM vault.credentials WHERE login_name='reviewer.one'").Scan(&user); err != nil {
		t.Fatal(err)
	}
	raw, authenticated, err := store.Create(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	status, body, cookies = request(mutation, "1", origin, &http.Cookie{Name: cookie.Name, Value: raw})
	if status != 200 || len(cookies) != 0 || !strings.Contains(body, authenticated.CSRFToken) {
		t.Fatal("authenticated session not reused")
	}
	current, err := store.LookupBrowserSession(ctx, raw)
	if err != nil || current.UserID != user || !current.IdleExpiresAt.Equal(authenticated.IdleExpiresAt) {
		t.Fatal("bootstrap changed identity or renewed idle expiry")
	}
	var before, after int
	if err = admin.QueryRow(ctx, "SELECT count(*) FROM vault.sessions").Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query, header, origin string
		status                int
		code                  string
	}{
		{mutation, "", origin, 200, "FORBIDDEN"},
		{mutation, "wrong", origin, 403, "FORBIDDEN"},
		{mutation, "1", "https://evil.example.com", 403, "FORBIDDEN"},
		{`query beginSession {serviceInfo{name}}`, "1", origin, 400, "INVALID_INPUT"},
		{`mutation {beginSession{csrfToken} __typename}`, "1", origin, 400, "INVALID_INPUT"},
		{`mutation {a:beginSession{csrfToken} b:beginSession{csrfToken}}`, "1", origin, 400, "INVALID_INPUT"},
		{`mutation {beginSession{unknown}}`, "1", origin, 400, "INVALID_INPUT"},
	} {
		got, payload, issued := request(tc.query, tc.header, tc.origin, nil)
		if got != tc.status || !strings.Contains(payload, tc.code) || len(issued) != 0 {
			t.Fatalf("rejection failed: %d %s", got, payload)
		}
	}
	if err = admin.QueryRow(ctx, "SELECT count(*) FROM vault.sessions").Scan(&after); err != nil || before != after {
		t.Fatal("rejected requests allocated sessions")
	}
	if err = store.Revoke(ctx, cookie.Value); err != nil {
		t.Fatal(err)
	}
	status, body, cookies = request(mutation, "1", origin, cookie)
	if status != 200 || len(cookies) != 1 || cookies[0].Value == cookie.Value || strings.Contains(body, `"errors"`) {
		t.Fatal("revoked cookie not replaced")
	}

	// A far-future window is treated as exhausted too: clock rollback must not reset
	// the allocation budget. Two independent pools exercise the shared row lock.
	reset := func() {
		t.Helper()
		if _, err := admin.Exec(ctx, "UPDATE vault.bootstrap_budget SET window_start=date_trunc('minute',clock_timestamp())+interval '1 hour',creations=0"); err != nil {
			t.Fatal(err)
		}
	}
	reset()
	otherPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer otherPool.Close()
	other, err := auth.NewSessionStore(otherPool, 24*time.Hour, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, s := range []*auth.SessionStore{store, other} {
		wg.Add(1)
		go func(s *auth.SessionStore) { defer wg.Done(); _, _, _, e := s.BeginSession(ctx, "", 1); results <- e }(s)
	}
	wg.Wait()
	close(results)
	success, limited := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, auth.ErrBootstrapLimited) {
			limited++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || limited != 1 {
		t.Fatalf("shared budget success=%d limited=%d", success, limited)
	}
	if _, _, created, e := store.BeginSession(ctx, raw, 1); e != nil || created {
		t.Fatal("reuse consumed exhausted budget")
	}
	if _, err = admin.Exec(ctx, "UPDATE vault.bootstrap_budget SET creations=600"); err != nil {
		t.Fatal(err)
	}
	status, body, cookies = request(mutation, "1", origin, nil)
	if status != 200 || !strings.Contains(body, "RATE_LIMITED") || len(cookies) != 0 {
		t.Fatal("limit not safely presented")
	}

	// Failed insertion must roll back both budget and session creation.
	reset()
	if _, err = admin.Exec(ctx, `CREATE FUNCTION vault.reject_bootstrap_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected private failure'; END $$;
 CREATE TRIGGER reject_bootstrap_test BEFORE INSERT ON vault.sessions FOR EACH ROW EXECUTE FUNCTION vault.reject_bootstrap_test()`); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = store.BeginSession(ctx, "", 1)
	if !errors.Is(err, auth.ErrSessionStore) {
		t.Fatal("insertion failure not private")
	}
	var used int
	if err = admin.QueryRow(ctx, "SELECT creations FROM vault.bootstrap_budget").Scan(&used); err != nil || used != 0 {
		t.Fatal("failed creation consumed budget")
	}
	if _, err = admin.Exec(ctx, `DROP TRIGGER reject_bootstrap_test ON vault.sessions; DROP FUNCTION vault.reject_bootstrap_test(); UPDATE vault.bootstrap_budget SET window_start=date_trunc('minute',clock_timestamp()),creations=0`); err != nil {
		t.Fatal(err)
	}
}
