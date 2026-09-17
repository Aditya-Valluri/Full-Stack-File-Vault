package auth

import (
	"strings"
	"testing"
)

func TestPasswordRoundTrip(t *testing.T) {
	p := []byte("a sufficiently long passphrase ")
	h, err := HashPassword(p)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := HashPassword(p)
	if err != nil {
		t.Fatal(err)
	}
	if h == h2 {
		t.Fatal("salt reused")
	}
	for _, tc := range []struct {
		value string
		want  bool
	}{{string(p), true}, {"wrong password", false}, {strings.TrimSpace(string(p)), false}} {
		got, err := VerifyPassword([]byte(tc.value), h)
		if err != nil || got != tc.want {
			t.Fatalf("got %v %v", got, err)
		}
	}
}
func TestInvalidPasswordsAndHashes(t *testing.T) {
	for _, p := range []string{"short", strings.Repeat("x", 1025), string([]byte{255})} {
		if _, err := HashPassword([]byte(p)); err == nil {
			t.Fatal("invalid password accepted")
		}
	}
	for _, h := range []string{"", hashPrefix + "bad$hash", strings.Replace(hashPrefix, "19456", "4294967295", 1) + "salt$key", strings.Repeat("x", 513)} {
		if _, err := VerifyPassword([]byte("a long password"), h); err == nil {
			t.Fatal("corrupt hash accepted")
		}
	}
}
func TestNormalizeLogin(t *testing.T) {
	got, err := NormalizeLogin(" Reviewer.One ")
	if err != nil || got != "reviewer.one" {
		t.Fatal(got, err)
	}
	for _, name := range []string{"ab", "_abc", "a@example.com", "résumé", strings.Repeat("a", 65)} {
		if _, err := NormalizeLogin(name); err == nil {
			t.Fatal("invalid login accepted")
		}
	}
}
