package auth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type browserStoreStub struct {
	state          Session
	err            error
	touchErr       error
	reads, touches int
}

func (s *browserStoreStub) LookupBrowserSession(context.Context, string) (Session, error) {
	s.reads++
	return s.state, s.err
}
func (s *browserStoreStub) LookupAndTouch(context.Context, string) (Session, error) {
	s.touches++
	return s.state, s.touchErr
}

type bodySpy struct{ reads int }

func (s *bodySpy) Read([]byte) (int, error) { s.reads++; return 0, io.EOF }
func (s *bodySpy) Close() error             { return nil }

func TestBrowserConfiguration(t *testing.T) {
	for _, tc := range []struct {
		origin     string
		dev, valid bool
	}{
		{"https://vault.example.com", false, true}, {"http://127.0.0.1:8080", true, true}, {"http://[::1]:8080", true, true},
		{"http://localhost:8080", true, true}, {"https://vault.example.com", true, true},
		{"http://vault.example.com", true, false}, {"http://127.0.0.1:8080", false, false},
		{"https://vault.example.com/", false, false}, {"https://user@vault.example.com", false, false},
		{"https://vault.example.com?", false, false}, {"https://vault.example.com#", false, false},
		{"https://*.example.com", false, false}, {"https://vault.example.com:99999", false, false}, {"", false, false},
	} {
		t.Run(tc.origin, func(t *testing.T) {
			err := ValidateBrowserConfig(BrowserConfig{Origin: tc.origin, Development: tc.dev})
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
}

func TestSessionCookieScope(t *testing.T) {
	for _, tc := range []struct {
		origin, name string
		dev, secure  bool
	}{
		{"https://vault.example.com", "__Host-vault_session", false, true},
		{"http://127.0.0.1:8080", "vault_session_dev", true, false},
	} {
		b, err := NewBrowserSecurity(BrowserConfig{Origin: tc.origin, Development: tc.dev}, &browserStoreStub{})
		if err != nil {
			t.Fatal(err)
		}
		token, err := newSessionToken()
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		expiry := time.Now().Add(time.Hour).Truncate(time.Second)
		if err = b.SetSessionCookie(w, token, expiry); err != nil {
			t.Fatal(err)
		}
		cookies := w.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatal("cookie missing")
		}
		c := cookies[0]
		if c.Name != tc.name || c.Value != token || c.Path != "/" || c.Domain != "" || c.Secure != tc.secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || !c.Expires.Equal(expiry) {
			t.Fatal("unsafe cookie scope")
		}
		cleared := httptest.NewRecorder()
		b.ClearSessionCookie(cleared)
		d := cleared.Result().Cookies()[0]
		if d.Name != c.Name || d.Path != c.Path || d.Domain != c.Domain || d.Secure != c.Secure || !d.HttpOnly || d.MaxAge != -1 || d.Value != "" {
			t.Fatal("deletion scope differs")
		}
		invalid := httptest.NewRecorder()
		if err = b.SetSessionCookie(invalid, "bad\r\ntoken", expiry); err == nil || invalid.Header().Get("Set-Cookie") != "" {
			t.Fatal("invalid token set")
		}
	}
}

func TestBrowserBoundary(t *testing.T) {
	const origin = "https://vault.example.com"
	csrf := strings.Repeat("A", 43)
	for _, tc := range []struct {
		name, origin, site, csrf                            string
		cookie, duplicateCookie, duplicateOrigin, anonymous bool
		storeErr, touchErr                                  error
		want, reads, touches                                int
	}{
		{name: "public", origin: origin, want: 204},
		{name: "missing origin", want: 403}, {name: "null", origin: "null", want: 403},
		{name: "suffix attack", origin: origin + ".evil.test", want: 403},
		{name: "wrong port", origin: origin + ":444", want: 403},
		{name: "duplicate origin", origin: origin, duplicateOrigin: true, want: 403},
		{name: "cross-site", origin: origin, site: "cross-site", want: 403},
		{name: "same-site sibling", origin: origin, site: "same-site", want: 403},
		{name: "missing csrf", origin: origin, cookie: true, want: 403},
		{name: "wrong csrf", origin: origin, cookie: true, csrf: strings.Repeat("B", 43), want: 403, reads: 1},
		{name: "valid authenticated", origin: origin, site: "same-origin", cookie: true, csrf: csrf, want: 204, reads: 1, touches: 1},
		{name: "valid anonymous", origin: origin, cookie: true, csrf: csrf, anonymous: true, want: 204, reads: 1},
		{name: "duplicate cookie", origin: origin, cookie: true, duplicateCookie: true, csrf: csrf, want: 401},
		{name: "revoked", origin: origin, cookie: true, csrf: csrf, storeErr: ErrInvalidSession, want: 401, reads: 1},
		{name: "database unavailable", origin: origin, cookie: true, csrf: csrf, storeErr: errors.New("private database secret"), want: 503, reads: 1},
		{name: "revoked during validation", origin: origin, cookie: true, csrf: csrf, touchErr: ErrInvalidSession, want: 401, reads: 1, touches: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := Session{UserID: "user", CSRFToken: csrf}
			if tc.anonymous {
				state.UserID = ""
			}
			store := &browserStoreStub{state: state, err: tc.storeErr, touchErr: tc.touchErr}
			b, err := NewBrowserSecurity(BrowserConfig{Origin: origin}, store)
			if err != nil {
				t.Fatal(err)
			}
			reached := false
			h := b.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				got, ok := BrowserSessionFromContext(r.Context())
				if tc.cookie && (!ok || got.UserID != state.UserID) {
					t.Error("session context missing")
				}
				if !tc.cookie && ok {
					t.Error("invented identity")
				}
				w.WriteHeader(204)
			}))
			body := &bodySpy{}
			r := httptest.NewRequest("POST", "/graphql", body)
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.duplicateOrigin {
				r.Header.Add("Origin", origin)
			}
			if tc.site != "" {
				r.Header.Set("Sec-Fetch-Site", tc.site)
			}
			if tc.csrf != "" {
				r.Header.Set("X-CSRF-Token", tc.csrf)
			}
			// A claimed bootstrap header never creates an exception in this micro-step.
			r.Header.Set("X-Vault-CSRF-Bootstrap", "1")
			if tc.cookie {
				r.AddCookie(&http.Cookie{Name: "__Host-vault_session", Value: "opaque"})
			}
			if tc.duplicateCookie {
				r.AddCookie(&http.Cookie{Name: "__Host-vault_session", Value: "another"})
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want || reached != (tc.want == 204) {
				t.Fatalf("status=%d reached=%v", w.Code, reached)
			}
			if store.reads != tc.reads || store.touches != tc.touches {
				t.Fatalf("reads=%d touches=%d", store.reads, store.touches)
			}
			if body.reads != 0 {
				t.Fatal("security checks read body")
			}
			if w.Header().Get("Access-Control-Allow-Origin") != "" || strings.Contains(w.Body.String(), "private database secret") {
				t.Fatal("unsafe response")
			}
			if tc.want != 204 && !strings.Contains(w.Body.String(), `"code"`) {
				t.Fatal("missing error code")
			}
		})
	}
}

func TestBrowserRejectsGETAndPreflight(t *testing.T) {
	b, _ := NewBrowserSecurity(BrowserConfig{Origin: "https://vault.example.com"}, &browserStoreStub{})
	for _, method := range []string{"GET", "OPTIONS"} {
		w := httptest.NewRecorder()
		b.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("request executed") })).ServeHTTP(w, httptest.NewRequest(method, "/graphql", nil))
		if w.Code != 405 || w.Header().Get("Allow") != "POST" {
			t.Fatal("unexpected method handling")
		}
	}
}
