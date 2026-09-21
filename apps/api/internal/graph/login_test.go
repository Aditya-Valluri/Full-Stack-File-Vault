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

	"full-stack-file-vault.local/api/internal/auth"
)

type loginTestSessions struct{ state auth.Session }

func (s loginTestSessions) LookupBrowserSession(context.Context, string) (auth.Session, error) {
	return s.state, nil
}
func (s loginTestSessions) LookupAndTouch(context.Context, string) (auth.Session, error) {
	return s.state, nil
}

func TestLoginOperationAndBrowserGuards(t *testing.T) {
	const field = `login(input:{loginName:"reviewer.one",password:"private-password"}){csrfToken user{id role}}`
	for _, tc := range []struct {
		name, query, cookie, csrf, bootstrap, origin string
		wantCalls                                    int
	}{
		{"valid", `mutation {` + field + `}`, "yes", "yes", "", "https://vault.example.com", 1},
		{"alias", `mutation {alias:` + field + `}`, "yes", "yes", "", "https://vault.example.com", 1},
		{"fragment", `mutation {...F} fragment F on Mutation {` + field + `}`, "yes", "yes", "", "https://vault.example.com", 1},
		{"mixed", `mutation {` + field + ` beginSession{csrfToken}}`, "yes", "yes", "", "https://vault.example.com", 0},
		{"duplicates", `mutation {a:` + field + ` b:` + field + `}`, "yes", "yes", "", "https://vault.example.com", 0},
		{"directive", `mutation { ...F @include(if:true)} fragment F on Mutation {` + field + `}`, "yes", "yes", "", "https://vault.example.com", 0},
		{"bootstrap bypass", `mutation {` + field + `}`, "yes", "yes", "1", "https://vault.example.com", 0},
		{"no cookie", `mutation {` + field + `}`, "", "yes", "", "https://vault.example.com", 0},
		{"no csrf", `mutation {` + field + `}`, "yes", "", "", "https://vault.example.com", 0},
		{"wrong origin", `mutation {` + field + `}`, "yes", "yes", "", "https://evil.example.com", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			csrf := strings.Repeat("c", 43)
			boundary, err := auth.NewBrowserSecurity(auth.BrowserConfig{Origin: "https://vault.example.com"}, loginTestSessions{auth.Session{CSRFToken: csrf}})
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			h := NewAuthenticationHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), boundary, func(context.Context, string) (string, auth.Session, bool, error) {
				t.Fatal("unexpected bootstrap")
				return "", auth.Session{}, false, nil
			}, func(ctx context.Context, anonymous, token, peer, name string, password []byte) (string, auth.Session, error) {
				calls++
				if peer != "192.0.2.1" || name != "reviewer.one" || string(password) != "private-password" {
					t.Fatal("wrong login input")
				}
				return strings.Repeat("A", 43), auth.Session{UserID: "user-id", Role: "USER", CSRFToken: strings.Repeat("d", 43), ExpiresAt: time.Now().Add(time.Hour)}, nil
			}, func(context.Context, string) error { t.Fatal("unexpected logout"); return nil })
			body, _ := json.Marshal(map[string]string{"query": tc.query})
			req := httptest.NewRequest("POST", "/graphql", bytes.NewReader(body))
			req.RemoteAddr = "192.0.2.1:1234"
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("X-Forwarded-For", "203.0.113.99")
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: "__Host-vault_session", Value: strings.Repeat("A", 43)})
			}
			if tc.csrf != "" {
				req.Header.Set("X-CSRF-Token", csrf)
			}
			if tc.bootstrap != "" {
				req.Header.Set("X-Vault-CSRF-Bootstrap", tc.bootstrap)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if calls != tc.wantCalls {
				t.Fatalf("calls=%d response=%s", calls, w.Body.String())
			}
			if tc.wantCalls == 1 && (len(w.Result().Cookies()) != 1 || strings.Contains(w.Body.String(), `"errors"`)) {
				t.Fatal("successful login did not issue cookie")
			}
			if tc.wantCalls == 0 && len(w.Result().Cookies()) != 0 {
				t.Fatal("rejected request set a cookie")
			}
		})
	}
}

func TestLoginValidationDoesNotEchoSecrets(t *testing.T) {
	const secret = "private-password-value"
	for _, body := range []string{
		`{"query":"mutation($i:LoginInput!){login(input:$i){csrfToken}}","variables":{"i":{"loginName":"reviewer.one","password":{"secret":"` + secret + `"}}}}`,
		`{"query":"mutation {login(input:{loginName:\"reviewer.one\",password:{secret:\"` + secret + `\"}}){csrfToken}}"}`,
	} {
		var logs bytes.Buffer
		h := NewHandler(slog.New(slog.NewTextHandler(&logs, nil)))
		req := httptest.NewRequest("POST", "/graphql", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if !strings.Contains(w.Body.String(), `"errors"`) || strings.Contains(w.Body.String(), secret) || strings.Contains(logs.String(), secret) {
			t.Fatal("unsafe validation response")
		}
	}
}
