package main

import (
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var contentPath = regexp.MustCompile("^/(shared-)?content/[A-Za-z0-9_-]{43}$")

// gateway exposes only application transport and static assets. Internal metrics
// stay on loopback. No access logs include bearer URLs or request headers.
func gateway(public string) http.Handler {
	target, _ := url.Parse("http://127.0.0.1:8080")
	proxy := &httputil.ReverseProxy{
		Rewrite:  func(r *httputil.ProxyRequest) { r.SetURL(target); r.Out.Host = r.In.Host },
		ErrorLog: log.New(io.Discard, "", 0),
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		},
	}
	client := &http.Client{Timeout: 2 * time.Second}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/graphql" || contentPath.MatchString(r.URL.Path) {
			proxy.ServeHTTP(w, r)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			w.Header().Set("Allow", "GET, HEAD")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path == "/readyz" {
			for _, endpoint := range []string{"http://127.0.0.1:8080/readyz", "http://127.0.0.1:8082/healthz"} {
				request, _ := http.NewRequestWithContext(r.Context(), "GET", endpoint, nil)
				response, err := client.Do(request)
				if err != nil {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				status := response.StatusCode
				response.Body.Close()
				if status != 200 {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		name := "index.html"
		if r.URL.Path != "/" && r.URL.Path != "/share" {
			if !strings.HasPrefix(r.URL.Path, "/assets/") {
				http.NotFound(w, r)
				return
			}
			base := strings.TrimPrefix(r.URL.Path, "/assets/")
			if base == "" || base == "." || base == ".." || strings.ContainsAny(base, "/\\") {
				http.NotFound(w, r)
				return
			}
			name = filepath.Join("assets", base)
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		file, err := os.Open(filepath.Join(public, name))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, info.Name(), info.ModTime(), file)
	})
}
