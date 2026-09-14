package config

import "testing"

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct{ name, url, addr string }{
		{"missing database", "", "127.0.0.1:8080"},
		{"missing port", "postgres://example", "localhost"},
		{"invalid port", "postgres://example", "localhost:65536"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", tc.url)
			t.Setenv("HTTP_ADDR", tc.addr)
			if _, err := Load(); err == nil {
				t.Fatal("expected configuration error")
			}
		})
	}
}
