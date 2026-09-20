package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestContentBrowserBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, method, origin, site string
		cookie, duplicate          bool
		status                     int
	}{
		{"navigation", "GET", "", "same-origin", true, false, 204},
		{"head", "HEAD", "", "none", true, false, 204},
		{"cross origin", "GET", "https://evil.example", "", true, false, 403},
		{"cross site", "GET", "", "cross-site", true, false, 403},
		{"no cookie", "GET", "", "", false, false, 401},
		{"duplicate cookie", "GET", "", "", true, true, 401},
		{"post", "POST", "", "", true, false, 405},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &browserStoreStub{state: Session{UserID: "user", Role: "USER"}}
			browser, err := NewBrowserSecurity(BrowserConfig{Origin: "https://vault.example.com"}, store)
			if err != nil {
				t.Fatal(err)
			}
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := RequireUser(r.Context()); err != nil {
					t.Fatal(err)
				}
				binding, err := SessionBinding(r.Context())
				if err != nil || len(binding) != 32 {
					t.Fatal("missing session binding")
				}
				w.WriteHeader(204)
			})
			request := httptest.NewRequest(tc.method, "https://vault.example.com/content/token", nil)
			if tc.origin != "" {
				request.Header.Set("Origin", tc.origin)
			}
			if tc.site != "" {
				request.Header.Set("Sec-Fetch-Site", tc.site)
			}
			if tc.cookie {
				request.AddCookie(&http.Cookie{Name: "__Host-vault_session", Value: strings.Repeat("A", 43)})
			}
			if tc.duplicate {
				request.AddCookie(&http.Cookie{Name: "__Host-vault_session", Value: strings.Repeat("A", 43)})
			}
			response := httptest.NewRecorder()
			browser.WrapContent(next).ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatal(response.Code, response.Body.String())
			}
		})
	}
}

func TestAnonymousContentBoundaryIsSharingOnly(t *testing.T) {
	store := &browserStoreStub{state: Session{CSRFToken: strings.Repeat("A", 43)}}
	browser, err := NewBrowserSecurity(BrowserConfig{Origin: "https://vault.example.com"}, store)
	if err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, _, err := BrowserBinding(r.Context()); err != nil {
			t.Fatal("anonymous binding missing", err)
		}
		if _, err := RequireUser(r.Context()); err == nil {
			t.Fatal("anonymous session became a user")
		}
		w.WriteHeader(204)
	})
	request := httptest.NewRequest("GET", "https://vault.example.com/shared-content/token", nil)
	request.AddCookie(&http.Cookie{Name: "__Host-vault_session", Value: strings.Repeat("A", 43)})
	for _, tc := range []struct {
		handler http.Handler
		want    int
	}{{browser.WrapSharedContent(next), 204}, {browser.WrapContent(next), 401}} {
		response := httptest.NewRecorder()
		tc.handler.ServeHTTP(response, request)
		if response.Code != tc.want {
			t.Fatal("content boundary mismatch", response.Code, tc.want)
		}
	}
}
