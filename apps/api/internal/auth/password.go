// Package auth supplies credential primitives, not HTTP authentication.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"golang.org/x/crypto/argon2"
	"strings"
)

const MaxPasswordBytes = 1024
const hashPrefix = "$argon2id$v=19$m=19456,t=2,p=1$"

// HashPassword preserves password bytes. The initial profile uses 19 MiB, two
// iterations and one lane; benchmark concurrency before exposing login publicly.
func HashPassword(password []byte) (string, error) {
	if err := ValidateNewPassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", errors.New("salt generation failed")
	}
	key := argon2.IDKey(password, salt, 2, 19456, 1, 32)
	defer clear(key)
	return hashPrefix + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}

// VerifyPassword accepts only the explicitly supported cost profile, preventing
// malformed stored parameters from triggering unbounded allocation or CPU work.
func VerifyPassword(password []byte, encoded string) (bool, error) {
	if len(password) > MaxPasswordBytes {
		return false, nil
	}
	if len(encoded) > 512 || !strings.HasPrefix(encoded, hashPrefix) {
		return false, errors.New("unsupported password hash")
	}
	parts := strings.Split(strings.TrimPrefix(encoded, hashPrefix), "$")
	if len(parts) != 2 {
		return false, errors.New("invalid password hash")
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[0])
	if err != nil || len(salt) != 16 {
		return false, errors.New("invalid password hash")
	}
	expected, err := base64.RawStdEncoding.Strict().DecodeString(parts[1])
	if err != nil || len(expected) != 32 {
		return false, errors.New("invalid password hash")
	}
	actual := argon2.IDKey(password, salt, 2, 19456, 1, 32)
	defer clear(actual)
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

// NormalizeLogin uses ASCII usernames, not email identities. Whitespace is trimmed
// and ASCII capitals folded; passwords are never normalized in this way.
func NormalizeLogin(name string) (string, error) {
	name = strings.TrimSpace(name)
	if len(name) < 3 || len(name) > 64 {
		return "", errors.New("login requires 3-64 ASCII characters")
	}
	b := []byte(name)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			c += 32
			b[i] = c
		}
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			continue
		}
		if i > 0 && (c == '.' || c == '_' || c == '-') {
			continue
		}
		return "", errors.New("invalid login name")
	}
	return string(b), nil
}
