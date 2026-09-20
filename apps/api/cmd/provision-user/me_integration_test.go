//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"file-vault.local/api/internal/auth"
	"file-vault.local/api/internal/graph"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testMe(t *testing.T, ctx context.Context, admin *pgx.Conn, dsn string) {
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
	h := graph.NewAuthenticationHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), boundary, func(ctx context.Context, token string) (string, auth.Session, bool, error) {
		return store.BeginSession(ctx, token, 60)
	}, store.LoginBrowser, store.Revoke)
	srv := httptest.NewTLSServer(h)
	defer srv.Close()
	request := func(query, token, csrf string) (int, string) {
		t.Helper()
		payload, _ := json.Marshal(map[string]string{"query": query})
		req, err := http.NewRequestWithContext(ctx, "POST", srv.URL, bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://vault.example.com")
		if token != "" {
			req.AddCookie(&http.Cookie{Name: "__Host-vault_session", Value: token})
		}
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("identity response cacheable")
		}
		return resp.StatusCode, string(body)
	}
	anon, anonymous, err := store.CreateAnonymous(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`{me{id role}}`, `query {alias:me{...U}} fragment U on AuthenticatedUser{id role}`, `{serviceInfo{name} me{id}}`} {
		for _, s := range []struct{ token, csrf string }{{"", ""}, {anon, anonymous.CSRFToken}} {
			status, body := request(q, s.token, s.csrf)
			if status != 200 || !strings.Contains(body, "UNAUTHENTICATED") {
				t.Fatalf("anonymous me accepted: %d %s", status, body)
			}
		}
	}
	var user, otherUser string
	if err = admin.QueryRow(ctx, "SELECT user_id::text FROM vault.credentials WHERE login_name='reviewer.one'").Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err = admin.QueryRow(ctx, "SELECT id::text FROM vault.users WHERE id<>$1 LIMIT 1", user).Scan(&otherUser); err != nil {
		t.Fatal(err)
	}
	token, state, err := store.Create(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, otherState, err := store.Create(ctx, otherUser)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []struct {
		token string
		state auth.Session
	}{{token, state}, {otherToken, otherState}} {
		status, body := request(`{me{id role}}`, s.token, s.state.CSRFToken)
		if status != 200 || strings.Contains(body, `"errors"`) || !strings.Contains(body, s.state.UserID) || !strings.Contains(body, s.state.Role) || strings.Contains(body, s.token) {
			t.Fatal("me returned incorrect identity")
		}
	}
	status, _ := request(`{me{id}}`, token, otherState.CSRFToken)
	if status != 403 {
		t.Fatal("cross-session CSRF accepted")
	}
	if _, err = admin.Exec(ctx, "UPDATE vault.users SET role='ADMIN' WHERE id=$1", user); err != nil {
		t.Fatal(err)
	}
	status, body := request(`{alias:me{id role}}`, token, state.CSRFToken)
	if status != 200 || !strings.Contains(body, `"role":"ADMIN"`) {
		t.Fatal("me returned stale role")
	}
	if _, err = admin.Exec(ctx, "UPDATE vault.users SET role='USER',disabled_at=clock_timestamp() WHERE id=$1", user); err != nil {
		t.Fatal(err)
	}
	status, _ = request(`{me{id role}}`, token, state.CSRFToken)
	if status != 401 {
		t.Fatal("disabled identity accepted")
	}
	if _, err = admin.Exec(ctx, "UPDATE vault.users SET disabled_at=NULL WHERE id=$1", user); err != nil {
		t.Fatal(err)
	}
	if err = store.Revoke(ctx, token); err != nil {
		t.Fatal(err)
	}
	status, _ = request(`{me{id role}}`, token, state.CSRFToken)
	if status != 401 {
		t.Fatal("revoked identity accepted")
	}
	pool.Close()
	status, _ = request(`{me{id role}}`, otherToken, otherState.CSRFToken)
	if status != 503 {
		t.Fatal("storage failure returned identity")
	}
}
