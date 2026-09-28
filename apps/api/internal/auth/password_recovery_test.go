package auth

import (
	"bytes"
	"testing"
)

func TestResetPurposeIsolation(t *testing.T) {
	token, _ := newSessionToken()
	registration, _ := verificationDigest(token, "0000001")
	reset, err := resetDigest(token, "0000001")
	if err != nil || bytes.Equal(reset, registration) {
		t.Fatal("reset code is not purpose separated")
	}
	other, _ := newSessionToken()
	different, _ := resetDigest(other, "0000001")
	if bytes.Equal(reset, different) {
		t.Fatal("reset not bound to browser")
	}
	if _, err := resetDigest(token, "12345678"); err != ErrResetRejected {
		t.Fatal("invalid reset format")
	}
}
