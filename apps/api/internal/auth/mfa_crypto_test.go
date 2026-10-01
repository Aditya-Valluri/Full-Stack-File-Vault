package auth

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"
)

func TestTOTPReferenceVectorsAndReplay(t *testing.T) {
	seed := []byte("12345678901234567890")
	for _, tc := range []struct {
		unix int64
		code string
	}{
		{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"},
		{1234567890, "005924"}, {2000000000, "279037"}, {20000000000, "353130"},
	} {
		step := tc.unix / 30
		if got := totpAt(seed, step); got != tc.code {
			t.Fatalf("reference vector failed at %d", tc.unix)
		}
		if _, ok := verifyTOTP(seed, tc.code, time.Unix(tc.unix, 0), step); ok {
			t.Fatal("replayed step accepted")
		}
		if _, ok := verifyTOTP(seed, tc.code, time.Unix(tc.unix, 0), -1); !ok {
			t.Fatal("valid code rejected")
		}
	}
	for _, invalid := range []string{"", "12345", "1234567", "abcdef"} {
		if _, ok := verifyTOTP(seed, invalid, time.Unix(59, 0), -1); ok {
			t.Fatal("invalid code accepted")
		}
	}
}
func TestMFAEncryptionBindingAndKeyValidation(t *testing.T) {
	store := &SessionStore{}
	if err := store.ConfigureMFA("malformed"); err == nil {
		t.Fatal("invalid key accepted")
	}
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatal(err)
	}
	if err := store.ConfigureMFA(base64.StdEncoding.EncodeToString(key[:])); err != nil {
		t.Fatal(err)
	}
	seed := []byte("12345678901234567890")
	first, err := store.encryptMFA("one", seed)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.encryptMFA("one", seed)
	if err != nil || bytes.Equal(first, second) || bytes.Contains(first, seed) {
		t.Fatal("nonce/encryption invariant")
	}
	plain, err := store.decryptMFA("one", first)
	if err != nil || !bytes.Equal(plain, seed) {
		t.Fatal("roundtrip")
	}
	if _, err = store.decryptMFA("two", first); err == nil {
		t.Fatal("cross-account ciphertext accepted")
	}
	first[len(first)-1] ^= 1
	if _, err = store.decryptMFA("one", first); err == nil {
		t.Fatal("modified ciphertext accepted")
	}
}
