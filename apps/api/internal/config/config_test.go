package config

import "testing"

func TestBootstrapBudgetConfiguration(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("PUBLIC_ORIGIN", "https://vault.example.com")
	t.Setenv("APP_ENV", "production")
	t.Setenv("HTTP_ADDR", "127.0.0.1:8080")
	for _, tc := range []struct {
		value string
		want  int
	}{{"", 60}, {"1", 1}, {"600", 600}, {"0", 0}, {"601", 0}, {"invalid", 0}} {
		t.Setenv("BOOTSTRAP_CREATIONS_PER_MINUTE", tc.value)
		cfg, err := Load()
		if tc.want == 0 {
			if err == nil {
				t.Fatal("invalid budget accepted")
			}
		} else if err != nil || cfg.BootstrapCreationsPerMinute != tc.want {
			t.Fatalf("budget %q: %v", tc.value, err)
		}
	}
}

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

func TestBrowserDeploymentConfiguration(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	for _, tc := range []struct {
		env, origin, addr string
		valid             bool
	}{
		{"", "https://vault.example.com", "0.0.0.0:8080", true},
		{"production", "http://localhost:8080", "127.0.0.1:8080", false},
		{"development", "http://localhost:8080", "127.0.0.1:8080", true},
		{"development", "http://localhost:8080", "0.0.0.0:8080", false},
		{"development", "http://localhost:8080", ":8080", false},
		{"production", "", "127.0.0.1:8080", false},
		{"typo", "https://vault.example.com", "127.0.0.1:8080", false},
	} {
		t.Run(tc.env+tc.origin+tc.addr, func(t *testing.T) {
			t.Setenv("APP_ENV", tc.env)
			t.Setenv("PUBLIC_ORIGIN", tc.origin)
			t.Setenv("HTTP_ADDR", tc.addr)
			_, err := Load()
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
}
