package main

import (
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCredentialsUseSeparateRolesAndTLS(t *testing.T) {
	t.Setenv("DEMO_OPERATOR_URL", "postgresql://operator:synthetic@db/vault")
	t.Setenv("DEMO_RUNTIME_SEED", strings.Repeat("r", 32))
	t.Setenv("DEMO_GC_SEED", strings.Repeat("g", 32))
	c, err := loadCredentials()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ dsn, role string }{{c.runtime, "vault_runtime"}, {c.gc, "vault_gc"}} {
		u, err := url.Parse(item.dsn)
		if err != nil || u.User.Username() != item.role || u.Query().Get("sslmode") != "require" {
			t.Fatal("incorrect restricted credential")
		}
		p, _ := u.User.Password()
		if len(p) != 64 {
			t.Fatal("invalid derived password")
		}
	}
	if c.runtimePassword == c.gcPassword {
		t.Fatal("worker secrets collide")
	}
	t.Setenv("DEMO_RUNTIME_SEED", "short")
	if _, err := loadCredentials(); err == nil {
		t.Fatal("weak seed accepted")
	}
}
func TestGatewayStaticBoundary(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><title>Vault</title>"), 0600); err != nil {
		t.Fatal(err)
	}
	h := gateway(dir)
	for _, path := range []string{"/", "/share", "/metrics", "/data/blobs/secret", "/assets/../secret", "/assets/", "/api/upload"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		want := 404
		if path == "/" || path == "/share" {
			want = 200
		}
		if w.Code != want {
			t.Fatalf("%s: %d", path, w.Code)
		}
		if w.Header().Get("Content-Security-Policy") == "" || w.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatal("security headers missing")
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/", nil))
	if w.Code != 405 {
		t.Fatal("static write accepted")
	}
}
