package graph

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
)

func TestLogoutBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, query                                  string
		anonymous, noCookie, noCSRF, bootstrap, fail bool
		calls                                        int
	}{
		{name: "valid", query: `mutation {logout}`, calls: 1},
		{name: "alias", query: `mutation {bye:logout}`, calls: 1},
		{name: "fragment", query: `mutation {...L} fragment L on Mutation {logout}`, calls: 1},
		{name: "missing identity", query: `mutation {logout}`, noCookie: true},
		{name: "anonymous", query: `mutation {logout}`, anonymous: true},
		{name: "csrf", query: `mutation {logout}`, noCSRF: true},
		{name: "bootstrap bypass", query: `mutation {logout}`, bootstrap: true},
		{name: "mixed", query: `mutation {logout beginSession{csrfToken}}`},
		{name: "duplicate", query: `mutation {a:logout b:logout}`},
		{name: "directive", query: `mutation {logout @skip(if:false)}`},
		{name: "storage failure", query: `mutation {logout}`, fail: true, calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token, csrf := strings.Repeat("A", 43), strings.Repeat("c", 43)
			state := auth.Session{UserID: "user", Role: "USER", CSRFToken: csrf}
			if tc.anonymous {
				state.UserID = ""
				state.Role = ""
			}
			boundary, err := auth.NewBrowserSecurity(auth.BrowserConfig{Origin: "https://vault.example.com"}, loginTestSessions{state})
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			h := NewAuthenticationHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), boundary,
				func(context.Context, string) (string, auth.Session, bool, error) {
					t.Fatal("unexpected bootstrap")
					return "", auth.Session{}, false, nil
				},
				func(context.Context, string, string, string, string, []byte) (string, auth.Session, error) {
					t.Fatal("unexpected login")
					return "", auth.Session{}, nil
				},
				func(ctx context.Context, raw string) error {
					calls++
					if raw != token {
						t.Fatal("wrong session revoked")
					}
					if tc.fail {
						return auth.ErrSessionStore
					}
					return nil
				})
			body, _ := json.Marshal(map[string]string{"query": tc.query})
			req := httptest.NewRequest("POST", "/graphql", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", "https://vault.example.com")
			if !tc.noCookie {
				req.AddCookie(&http.Cookie{Name: "__Host-vault_session", Value: token})
			}
			if !tc.noCSRF {
				req.Header.Set("X-CSRF-Token", csrf)
			}
			if tc.bootstrap {
				req.Header.Set("X-Vault-CSRF-Bootstrap", "1")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if calls != tc.calls {
				t.Fatalf("calls=%d response=%s", calls, w.Body.String())
			}
			cookies := w.Result().Cookies()
			if tc.calls == 1 && !tc.fail {
				if len(cookies) != 1 {
					t.Fatal("missing clearing cookie")
				}
				c := cookies[0]
				if c.Name != "__Host-vault_session" || c.Value != "" || c.MaxAge != -1 || !c.Expires.Before(time.Now()) || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Domain != "" {
					t.Fatal("incorrect cookie deletion scope")
				}
				if strings.Contains(w.Body.String(), `"errors"`) || !strings.Contains(w.Body.String(), "true") {
					t.Fatal("logout not successful")
				}
			} else if len(cookies) != 0 {
				t.Fatal("cookie cleared without successful revocation")
			}
			if tc.fail && !strings.Contains(w.Body.String(), "INTERNAL_ERROR") {
				t.Fatal("unsafe storage error")
			}
		})
	}
}
