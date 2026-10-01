package main

import (
	"reflect"
	"testing"
)

func TestAPIAuthEnvironmentIsolation(t *testing.T) {
	input := map[string]string{
		"MFA_ENCRYPTION_KEY":         "test-key",
		"MFA_ENCRYPTION_KEY_FILE":    "/test/key",
		"AUTH_MAIL_MODE":             "gmail",
		"GMAIL_SENDER_REFRESH_TOKEN": "test-token",
		"DEMO_OPERATOR_URL":          "must-not-forward",
		"DEMO_ADMIN_PASSWORD":        "must-not-forward",
		"GC_DATABASE_URL":            "must-not-forward",
	}
	got := appendAPIAuthEnvironment([]string{"APP_ENV=production"}, func(key string) (string, bool) {
		value, ok := input[key]
		return value, ok
	})
	want := []string{"APP_ENV=production", "AUTH_MAIL_MODE=gmail", "GMAIL_SENDER_REFRESH_TOKEN=test-token", "MFA_ENCRYPTION_KEY=test-key", "MFA_ENCRYPTION_KEY_FILE=/test/key"}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("API authentication environment did not match the explicit allowlist")
	}
	// Preserve both sources so the API can reject conflicting configuration.
	got = appendAPIAuthEnvironment(nil, func(string) (string, bool) { return "", false })
	if len(got) != 0 {
		t.Fatal("missing configuration must not introduce defaults")
	}
}
