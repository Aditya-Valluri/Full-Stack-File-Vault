package auth

import (
	_ "embed"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrWeakPassword = errors.New("choose a less predictable password with at least 15 characters")

//go:embed passworddata/common.txt
var commonPasswordData string

func passwordComparison(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, value)
}

var commonPasswords = func() map[string]bool {
	result := make(map[string]bool)
	for _, p := range strings.Fields(commonPasswordData) {
		result[passwordComparison(p)] = true
	}
	for _, p := range strings.Fields("fullstackfilevault filevault safeandsecure thisismypassword mypasswordispassword correcthorsebatterystaple") {
		result[p] = true
	}
	return result
}()

// New passwords only. Hashing preserves original bytes; existing login never
// applies this policy. This is a local denylist, not an exhaustive breach check.
func ValidateNewPassword(password []byte) error {
	if len(password) > MaxPasswordBytes || !utf8.Valid(password) || utf8.RuneCount(password) < 15 {
		return ErrWeakPassword
	}
	value := strings.TrimSpace(string(password))
	if utf8.RuneCountInString(value) < 15 {
		return ErrWeakPassword
	}
	compact := passwordComparison(value)
	if compact == "" || commonPasswords[compact] {
		return ErrWeakPassword
	}
	base := strings.Trim(compact, "0123456789")
	if commonPasswords[base] {
		return ErrWeakPassword
	}
	replaced := strings.NewReplacer("0", "o", "1", "i", "3", "e", "4", "a", "5", "s", "7", "t").Replace(base)
	if commonPasswords[replaced] {
		return ErrWeakPassword
	}
	decorated := strings.TrimFunc(strings.ToLower(value), func(r rune) bool { return !unicode.IsLetter(r) })
	decorated = strings.NewReplacer("@", "a", "$", "s", "0", "o", "1", "i", "3", "e", "4", "a", "5", "s", "7", "t").Replace(decorated)
	if commonPasswords[passwordComparison(decorated)] {
		return ErrWeakPassword
	}
	runes := []rune(compact)
	for period := 1; period <= 10 && period*2 <= len(runes); period++ {
		repeated := true
		for i := period; i < len(runes); i++ {
			if runes[i] != runes[i%period] {
				repeated = false
				break
			}
		}
		if repeated {
			return ErrWeakPassword
		}
	}
	for _, sequence := range []string{"0123456789", "9876543210", "abcdefghijklmnopqrstuvwxyz", "zyxwvutsrqponmlkjihgfedcba", "qwertyuiopasdfghjklzxcvbnm", "mnbvcxzlkjhgfdsapoiuytrewq"} {
		if strings.Contains(strings.Repeat(sequence, 100), compact) {
			return ErrWeakPassword
		}
	}
	return nil
}
