package auth

import "testing"

func TestNormalizeEmail(t *testing.T) {
	for input, want := range map[string]string{" Alice+tag@Example.com ": "alice+tag@example.com", "a.b@example.com": "a.b@example.com"} {
		got, err := NormalizeEmail(input)
		if err != nil || got != want {
			t.Fatalf("email normalization: %q %v", got, err)
		}
	}
	for _, input := range []string{"", "alice", "Alice <alice@example.com>", "a@example.com\r\nBcc:b@example.com", "é@example.com", "a b@example.com"} {
		if _, err := NormalizeEmail(input); err == nil {
			t.Fatalf("accepted invalid email %q", input)
		}
	}
	if got, err := normalizeIdentifier(" Legacy.User "); err != nil || got != "legacy.user" {
		t.Fatal("legacy normalization changed")
	}
}
