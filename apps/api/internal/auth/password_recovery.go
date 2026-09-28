package auth

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"full-stack-file-vault.local/api/internal/mailer"
	"github.com/jackc/pgx/v5"
)

var ErrResetRejected = errors.New("reset code invalid or expired")
var ErrPasswordRejected = errors.New("current password not accepted")

func resetDigest(token, code string) ([]byte, error) {
	// Reuse numeric format validation, but never accept a registration digest.
	if _, err := verificationDigest(token, code); err != nil {
		return nil, ErrResetRejected
	}
	mac := hmac.New(sha256.New, []byte(token))
	_, _ = mac.Write([]byte("password-reset-v1:" + code))
	return mac.Sum(nil), nil
}

func (s *SessionStore) hashNewPassword(ctx context.Context, password []byte) (string, error) {
	select {
	case s.hashSlots <- struct{}{}:
	case <-ctx.Done():
		return "", ErrSessionStore
	}
	defer func() { <-s.hashSlots }()
	return HashPassword(password)
}

// Send the same fixed message even for an ineligible email. Its challenge has
// no user target and can never reset/create an account. Responses and mail work
// do not reveal whether the supplied address has a password identity.
func (s *SessionStore) RequestPasswordReset(ctx context.Context, token, csrf, peer, email string) error {
	if s.mail == nil {
		return ErrEmailDisabled
	}
	if err := s.checkAnonymous(ctx, token, csrf); err != nil {
		return err
	}
	if err := s.reserveLoginAttempt(ctx, peer, email); err != nil {
		return err
	}
	normalized, err := NormalizeEmail(email)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	code, err := newVerificationCode()
	if err != nil {
		return err
	}
	digest, _ := resetDigest(token, code)
	binding, _ := sessionDigest(token)
	var issued bool
	if err = s.pool.QueryRow(ctx, "SELECT vault.issue_password_reset($1,$2,$3)", digest, binding, normalized).Scan(&issued); err != nil {
		return ErrSessionStore
	}
	if !issued {
		return ErrLoginForbidden
	}
	if err = s.mail.Send(ctx, mailer.Message{To: strings.TrimSpace(email), Purpose: mailer.ResetPassword, Code: code}); err != nil {
		cleanup, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		_, _ = s.pool.Exec(cleanup, "SELECT vault.discard_password_reset($1,$2)", digest, binding)
		return mailer.ErrDelivery
	}
	return nil
}

func (s *SessionStore) CompletePasswordReset(ctx context.Context, token, csrf, peer, code string, password []byte) error {
	if s.mail == nil {
		return ErrEmailDisabled
	}
	if err := s.checkAnonymous(ctx, token, csrf); err != nil {
		return err
	}
	key := sha256.Sum256([]byte("reset:" + peer))
	if err := s.reserveLoginAttempt(ctx, peer, hex.EncodeToString(key[:])); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	binding, _ := sessionDigest(token)
	var expected []byte
	// Independently committed attempt counts survive incorrect guesses or rollback.
	if err := s.pool.QueryRow(ctx, "SELECT vault.claim_password_reset_attempt($1)", binding).Scan(&expected); err != nil {
		return ErrSessionStore
	}
	digest, err := resetDigest(token, code)
	if err != nil || len(expected) != 32 || subtle.ConstantTimeCompare(digest, expected) != 1 {
		return ErrResetRejected
	}
	hash, err := s.hashNewPassword(ctx, password)
	if err != nil {
		return err
	}
	var changed bool
	if err = s.pool.QueryRow(ctx, "SELECT vault.complete_password_reset($1,$2,$3)", digest, binding, hash).Scan(&changed); err != nil {
		return ErrSessionStore
	}
	if !changed {
		return ErrResetRejected
	}
	return nil
}

// Both legacy and email-password identities retain their user UUID, files,
// quota and role. All sessions and outstanding reset grants are revoked.
func (s *SessionStore) ChangePassword(ctx context.Context, token, csrf, peer string, current, next []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	state, err := s.LookupBrowserSession(ctx, token)
	if err != nil || state.UserID == "" || subtle.ConstantTimeCompare([]byte(csrf), []byte(state.CSRFToken)) != 1 {
		return ErrUnauthenticated
	}
	if err = s.reserveLoginAttempt(ctx, peer, state.LoginName); err != nil {
		return err
	}
	var method, oldHash string
	err = s.pool.QueryRow(ctx, "SELECT 'legacy',password_hash FROM vault.credentials WHERE user_id=$1 UNION ALL SELECT 'password',password_hash FROM vault.user_identities WHERE user_id=$1 AND provider='password'", state.UserID).Scan(&method, &oldHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPasswordRejected
	}
	if err != nil {
		return ErrSessionStore
	}
	select {
	case s.hashSlots <- struct{}{}:
	case <-ctx.Done():
		return ErrSessionStore
	}
	ok, err := VerifyPassword(current, oldHash)
	<-s.hashSlots
	if err != nil {
		return ErrSessionStore
	}
	if !ok {
		return ErrPasswordRejected
	}
	if bytes.Equal(current, next) {
		return ErrWeakPassword
	}
	hash, err := s.hashNewPassword(ctx, next)
	if err != nil {
		return err
	}
	binding, _ := sessionDigest(token)
	var changed bool
	if err = s.pool.QueryRow(ctx, "SELECT vault.change_account_password($1,$2,$3,$4,$5)", state.UserID, binding, method, oldHash, hash).Scan(&changed); err != nil {
		return ErrSessionStore
	}
	if !changed {
		return ErrPasswordRejected
	}
	return nil
}

type passwordContextKey struct{}
type passwordCapability struct {
	request  func(context.Context, string) error
	complete func(context.Context, string, []byte) error
	change   func(context.Context, []byte, []byte) error
}

func RequestPasswordResetFromContext(ctx context.Context, email string) error {
	c, ok := ctx.Value(passwordContextKey{}).(passwordCapability)
	if !ok || c.request == nil {
		return ErrLoginForbidden
	}
	return c.request(ctx, email)
}
func CompletePasswordResetFromContext(ctx context.Context, code string, password []byte) error {
	c, ok := ctx.Value(passwordContextKey{}).(passwordCapability)
	if !ok || c.complete == nil {
		return ErrLoginForbidden
	}
	return c.complete(ctx, code, password)
}
func ChangePasswordFromContext(ctx context.Context, current, next []byte) error {
	c, ok := ctx.Value(passwordContextKey{}).(passwordCapability)
	if !ok || c.change == nil {
		return ErrUnauthenticated
	}
	return c.change(ctx, current, next)
}
