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

	"file-vault.local/api/internal/auth"
	"file-vault.local/api/internal/graph"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testLogout(t *testing.T, ctx context.Context, admin *pgx.Conn, dsn string) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	replicaPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer replicaPool.Close()
	store, err := auth.NewSessionStore(pool, 24*time.Hour, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	replica, err := auth.NewSessionStore(replicaPool, 24*time.Hour, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	boundary, err := auth.NewBrowserSecurity(auth.BrowserConfig{Origin: "https://vault.example.com"}, store)
	if err != nil {
		t.Fatal(err)
	}
	h := graph.NewAuthenticationHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), boundary, func(ctx context.Context, token string) (string, auth.Session, bool, error) {
		return store.BeginSession(ctx, token, 60)
	}, store.LoginBrowser, store.Revoke)
	srv := httptest.NewTLSServer(h)
	defer srv.Close()
	var user string
	if err = admin.QueryRow(ctx, "SELECT user_id::text FROM vault.credentials WHERE login_name='reviewer.one'").Scan(&user); err != nil {
		t.Fatal(err)
	}
	create := func() (string, auth.Session) {
		t.Helper()
		token, state, e := store.Create(ctx, user)
		if e != nil {
			t.Fatal(e)
		}
		return token, state
	}
	request := func(token, csrf string) (int, string, []*http.Cookie) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, "POST", srv.URL, strings.NewReader(`{"query":"mutation {logout}"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://vault.example.com")
		req.Header.Set("X-CSRF-Token", csrf)
		req.AddCookie(&http.Cookie{Name: "__Host-vault_session", Value: token})
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		payload, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable logout")
		}
		return resp.StatusCode, string(payload), resp.Cookies()
	}
	token, state := create()
	other, _ := create()
	status, body, cookies := request(token, strings.Repeat("x", 43))
	if status != 403 || len(cookies) != 0 {
		t.Fatal("CSRF rejection changed cookie")
	}
	if _, err = replica.LookupAndTouch(ctx, token); err != nil {
		t.Fatal("CSRF rejection revoked session")
	}
	// A real database trigger makes revocation fail after middleware authorization.
	if _, err = admin.Exec(ctx, `CREATE FUNCTION vault.reject_logout_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private revoke failure'; END $$;
 CREATE TRIGGER reject_logout_test BEFORE UPDATE OF revoked_at ON vault.sessions FOR EACH ROW EXECUTE FUNCTION vault.reject_logout_test()`); err != nil {
		t.Fatal(err)
	}
	status, body, cookies = request(token, state.CSRFToken)
	if status != 200 || !strings.Contains(body, "INTERNAL_ERROR") || len(cookies) != 0 || strings.Contains(body, "private") {
		t.Fatal("revocation failure reported success or cleared cookie")
	}
	if _, err = replica.LookupAndTouch(ctx, token); err != nil {
		t.Fatal("failed revocation changed session")
	}
	if _, err = admin.Exec(ctx, `DROP TRIGGER reject_logout_test ON vault.sessions; DROP FUNCTION vault.reject_logout_test()`); err != nil {
		t.Fatal(err)
	}
	// Activity on another connection races logout; neither ordering may resurrect it.
	start := make(chan struct{})
	result := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); <-start; _, e := replica.LookupAndTouch(ctx, token); result <- e }()
	close(start)
	status, body, cookies = request(token, state.CSRFToken)
	wg.Wait()
	e := <-result
	if e != nil && !errors.Is(e, auth.ErrInvalidSession) {
		t.Fatal(e)
	}
	var response struct {
		Data struct {
			Logout bool `json:"logout"`
		} `json:"data"`
	}
	if err = json.Unmarshal([]byte(body), &response); err != nil || status != 200 || !response.Data.Logout || len(cookies) != 1 || cookies[0].MaxAge != -1 {
		t.Fatal("logout failed")
	}
	if _, err = replica.LookupAndTouch(ctx, token); !errors.Is(err, auth.ErrInvalidSession) {
		t.Fatal("logout did not invalidate replica")
	}
	if _, err = replica.LookupAndTouch(ctx, other); err != nil {
		t.Fatal("logout revoked another session")
	}
	status, _, cookies = request(token, state.CSRFToken)
	if status != 401 || len(cookies) != 1 || cookies[0].MaxAge != -1 {
		t.Fatal("revoked cookie replay not rejected")
	}
}
