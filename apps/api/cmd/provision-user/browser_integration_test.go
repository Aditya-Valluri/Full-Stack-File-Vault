//go:build integration

package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"file-vault.local/api/internal/auth"
	"file-vault.local/api/internal/graph"
	"file-vault.local/api/internal/server"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testBrowserSecurity(t *testing.T, ctx context.Context, admin *pgx.Conn, dsn string) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal("pool failed")
	}
	defer pool.Close()
	store, err := auth.NewSessionStore(pool, 24*time.Hour, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var user string
	if err = admin.QueryRow(ctx, "SELECT user_id::text FROM vault.credentials WHERE login_name='reviewer.one'").Scan(&user); err != nil {
		t.Fatal("test user missing")
	}
	token, state, err := store.Create(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	_, other, err := store.Create(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	anonymous, anonState, err := store.CreateAnonymous(ctx)
	if err != nil {
		t.Fatal(err)
	}
	boundary, err := auth.NewBrowserSecurity(auth.BrowserConfig{Origin: "https://vault.example.com"}, store)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New("", func(context.Context) error { return nil }, logger, boundary.Wrap(graph.NewHandler(logger)))
	httpServer := httptest.NewTLSServer(srv.Handler)
	defer httpServer.Close()
	request := func(cookie, csrf, origin string, want int) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, "POST", httpServer.URL+"/graphql", strings.NewReader(`{"query":"{serviceInfo{name}}"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: "__Host-vault_session", Value: cookie})
		}
		response, err := httpServer.Client().Do(req)
		if err != nil {
			t.Fatal("HTTP request failed")
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal("body read failed")
		}
		if response.StatusCode != want {
			t.Fatalf("got status %d, want %d", response.StatusCode, want)
		}
		if want == 200 && !strings.Contains(string(body), "serviceInfo") {
			t.Fatal("GraphQL not reached")
		}
		for _, secret := range []string{token, state.CSRFToken, anonymous, anonState.CSRFToken} {
			if strings.Contains(string(body), secret) {
				t.Fatal("session secret in response")
			}
		}
	}
	request("", "", "", 403)
	request("", "", "https://vault.example.com", 200)
	request(token, other.CSRFToken, "https://vault.example.com", 403)
	request(token, state.CSRFToken, "https://evil.example.com", 403)
	request(token, state.CSRFToken, "https://vault.example.com", 200)
	request(anonymous, anonState.CSRFToken, "https://vault.example.com", 200)
	if err = store.Revoke(ctx, token); err != nil {
		t.Fatal(err)
	}
	request(token, state.CSRFToken, "https://vault.example.com", 401)
	pool.Close()
	request(anonymous, anonState.CSRFToken, "https://vault.example.com", 503)
	t.Log("TLS HTTP -> chi -> Origin/CSRF boundary -> real session store -> gqlgen checks passed")
}
