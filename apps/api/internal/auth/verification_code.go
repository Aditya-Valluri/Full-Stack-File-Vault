package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"math/big"
)

func newVerificationCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(10000000))
	if err != nil {
		return "", ErrSessionStore
	}
	return fmt.Sprintf("%07d", n.Int64()), nil
}

// Bind the small code space to the high-entropy HttpOnly browser token, which
// is not stored in PostgreSQL. Session bearer tokens remain 256-bit values.
func verificationDigest(session, code string) ([]byte, error) {
	if len(code) != 7 {
		return nil, ErrEmailVerification
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return nil, ErrEmailVerification
		}
	}
	if _, err := sessionDigest(session); err != nil {
		return nil, ErrEmailVerification
	}
	mac := hmac.New(sha256.New, []byte(session))
	_, _ = mac.Write([]byte("email-verification-v1:" + code))
	return mac.Sum(nil), nil
}
