package auth

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestSessionTokens(t *testing.T) {
	a, err := newSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	b, err := newSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("token reuse")
	}
	digest, err := sessionDigest(a)
	if err != nil || len(digest) != 32 || bytes.Equal(digest, []byte(a)) {
		t.Fatal("invalid digest")
	}
	for _, token := range []string{"", a + "=", strings.Repeat("!", 43), strings.Repeat("a", 10000)} {
		if _, err := sessionDigest(token); err == nil {
			t.Fatal("invalid token accepted")
		}
	}
}
func TestSessionStoreRequiresPool(t *testing.T) {
	if _, err := NewSessionStore(nil, 24*time.Hour, 30*time.Minute); err == nil {
		t.Fatal("nil pool accepted")
	}
}
