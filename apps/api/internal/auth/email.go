package auth

import (
	"errors"
	"net/mail"
	"strings"
)

// NormalizeEmail deliberately supports ASCII mailboxes only. We do not strip
// dots or plus suffixes. Case folding is the application's uniqueness policy.
func NormalizeEmail(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) < 3 || len(value) > 254 {
		return "", ErrEmailInput
	}
	for _, ch := range value {
		if ch < 33 || ch > 126 {
			return "", ErrEmailInput
		}
	}
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Address != value || parsed.Name != "" {
		return "", ErrEmailInput
	}
	return strings.ToLower(value), nil
}

var ErrEmailInput = errors.New("invalid email registration input")
var ErrEmailVerification = errors.New("verification invalid or expired")
var ErrEmailDisabled = errors.New("email registration unavailable")

func normalizeIdentifier(value string) (string, error) {
	if strings.Contains(value, "@") {
		return NormalizeEmail(value)
	}
	return NormalizeLogin(value)
}
