package sharing

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestOpaqueShareTokens(t *testing.T) {
	token, digest, err := newToken()
	if err != nil || len(token) != 43 || len(digest) != 32 {
		t.Fatal("invalid generated token", err)
	}
	decoded, err := tokenDigest(token)
	if err != nil || string(decoded) != string(digest) {
		t.Fatal("digest mismatch", err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(token)
	if string(raw) == string(digest) {
		t.Fatal("raw token persisted")
	}
	for _, invalid := range []string{"", strings.Repeat("A", 42), strings.Repeat("A", 44), strings.Repeat("A", 42) + "B", strings.Repeat("/", 43), token + "="} {
		if _, err := tokenDigest(invalid); err == nil {
			t.Fatal("noncanonical token accepted")
		}
	}
}
