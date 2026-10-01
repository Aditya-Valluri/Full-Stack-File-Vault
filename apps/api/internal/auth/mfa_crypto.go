package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"full-stack-file-vault.local/api/internal/secret"
)

var (
	ErrMFARequired    = errors.New("second factor required")
	ErrMFARejected    = errors.New("second factor rejected")
	ErrMFAUnavailable = errors.New("MFA unavailable")
)

// ConfigureMFA is startup-only. An absent key disables enrollment, not checks
// for already-enrolled accounts: those fail closed if the key is unavailable.
func (s *SessionStore) ConfigureMFA(encoded string) error {
	if encoded == "" {
		return nil
	}
	key, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(key) != 32 {
		clear(key)
		return ErrMFAUnavailable
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return ErrMFAUnavailable
	}
	s.mfaCipher, err = cipher.NewGCM(block)
	return err
}
func (s *SessionStore) ConfigureMFAFromEnvironment() error {
	value, err := secret.Read("MFA_ENCRYPTION_KEY")
	if err != nil {
		return ErrMFAUnavailable
	}
	return s.ConfigureMFA(value)
}
func (s *SessionStore) encryptMFA(user string, seed []byte) ([]byte, error) {
	if s.mfaCipher == nil || len(seed) != 20 {
		return nil, ErrMFAUnavailable
	}
	nonce := make([]byte, s.mfaCipher.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, ErrMFAUnavailable
	}
	result := append([]byte{1}, nonce...)
	return s.mfaCipher.Seal(result, nonce, seed, []byte("vault-mfa-v1:"+user)), nil
}
func (s *SessionStore) decryptMFA(user string, encrypted []byte) ([]byte, error) {
	if s.mfaCipher == nil || len(encrypted) != 49 || encrypted[0] != 1 {
		return nil, ErrMFAUnavailable
	}
	n := s.mfaCipher.NonceSize()
	seed, err := s.mfaCipher.Open(nil, encrypted[1:1+n], encrypted[1+n:], []byte("vault-mfa-v1:"+user))
	if err != nil {
		return nil, ErrMFAUnavailable
	}
	return seed, nil
}

// RFC 6238 / RFC 4226, SHA-1, six digits, thirty-second steps.
// HMAC-SHA1 is used for authenticator compatibility, never for password storage.
func totpAt(seed []byte, step int64) string {
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))
	mac := hmac.New(sha1.New, seed)
	_, _ = mac.Write(counter[:])
	digest := mac.Sum(nil)
	offset := digest[len(digest)-1] & 15
	value := binary.BigEndian.Uint32(digest[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1000000)
}
func verifyTOTP(seed []byte, code string, now time.Time, last int64) (int64, bool) {
	if len(code) != 6 {
		return 0, false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	current := now.Unix() / 30
	for _, step := range []int64{current, current - 1, current + 1} {
		if step > last && step >= 0 && subtle.ConstantTimeCompare([]byte(totpAt(seed, step)), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}
func recoveryDigest(user, code string) ([]byte, bool) {
	code = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	raw, err := hex.DecodeString(code)
	if err != nil || len(raw) != 16 {
		return nil, false
	}
	defer clear(raw)
	digest := sha256.Sum256([]byte("vault-mfa-recovery-v1:" + user + ":" + code))
	return digest[:], true
}
