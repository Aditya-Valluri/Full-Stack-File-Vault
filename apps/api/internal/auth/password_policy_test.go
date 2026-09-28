package auth

import (
	"bytes"
	"encoding/base64"
	"golang.org/x/crypto/argon2"
	"strings"
	"testing"
)

func TestNewPasswordPolicy(t *testing.T) {
	for _, p := range []string{"Password123456789!", "passwordpassword", "111111111111111", "abcdefghijklmnop", "qwertyuiopasdfghjkl", "FullStackFileVault2026!", "P@ssword123456789!", "                ", "correct horse battery staple"} {
		if err := ValidateNewPassword([]byte(p)); err != ErrWeakPassword {
			t.Errorf("predictable password accepted")
		}
	}
	for _, p := range []string{"orchard velvet lantern compass", "an independently chosen long phrase", "vZ8!uH4?mQ2#rL9%"} {
		if err := ValidateNewPassword([]byte(p)); err != nil {
			t.Errorf("acceptable password rejected: %v", err)
		}
	}
	if len(commonPasswords) < 9000 {
		t.Fatal("common-password data missing")
	}
	// New policy must not lock out an existing legacy account with an older hash.
	salt := bytes.Repeat([]byte{3}, 16)
	password := []byte(strings.Repeat("a", 15))
	key := argon2.IDKey(password, salt, 2, 19456, 1, 32)
	encoded := hashPrefix + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key)
	if ok, err := VerifyPassword(password, encoded); err != nil || !ok {
		t.Fatal("legacy hash no longer verifies")
	}
}
func TestVerificationCodeBinding(t *testing.T) {
	session, _ := newSessionToken()
	other, _ := newSessionToken()
	for i := 0; i < 100; i++ {
		code, err := newVerificationCode()
		if err != nil || len(code) != 7 {
			t.Fatal("code format")
		}
		for _, c := range code {
			if c < '0' || c > '9' {
				t.Fatal("non-digit")
			}
		}
	}
	first, err := verificationDigest(session, "0000001")
	if err != nil {
		t.Fatal(err)
	}
	second, _ := verificationDigest(other, "0000001")
	if bytes.Equal(first, second) {
		t.Fatal("code not session-bound")
	}
	for _, invalid := range []string{"123456", "12345678", "１２３４５６７", "123 456", "abcdefg"} {
		if _, err := verificationDigest(session, invalid); err == nil {
			t.Fatal("invalid code accepted")
		}
	}
}
