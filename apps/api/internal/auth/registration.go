package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
	"time"

	"full-stack-file-vault.local/api/internal/mailer"
	"github.com/jackc/pgx/v5"
)

// ConfigureMail is called once at startup, before requests are served.
func (s *SessionStore) ConfigureMail(sender mailer.MailSender) { s.mail = sender }
func (s *SessionStore) EmailRegistrationEnabled() bool         { return s.mail != nil }

func (s *SessionStore) checkAnonymous(ctx context.Context, token, csrf string) error {
	state, err := s.LookupAnonymous(ctx, token)
	if err != nil {
		return ErrLoginForbidden
	}
	if subtle.ConstantTimeCompare([]byte(state.CSRFToken), []byte(csrf)) != 1 {
		return ErrLoginForbidden
	}
	return nil
}

// RequestEmailRegistration never discloses whether an email already has an
// account. Existing addresses receive the same message, but cannot create/link
// another account. Shared budgets bound delivery, hashing and challenge growth.
func (s *SessionStore) RequestEmailRegistration(ctx context.Context, token, csrf, peer, email string) error {
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
	code, err := newVerificationCode()
	if err != nil {
		return err
	}
	digest, _ := verificationDigest(token, code)
	binding, _ := sessionDigest(token)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if _, err = s.pool.Exec(ctx, "DELETE FROM vault.email_challenges WHERE expires_at<=clock_timestamp()"); err != nil {
		return ErrSessionStore
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ErrSessionStore
	}
	defer rollbackSession(tx)
	if _, err = tx.Exec(ctx, "DELETE FROM vault.email_challenges WHERE binding_hash=$1", binding); err != nil {
		return ErrSessionStore
	}
	if _, err = tx.Exec(ctx, "INSERT INTO vault.email_challenges(token_hash,email_address,email_normalized,binding_hash) VALUES($1,$2,$3,$4)", digest, strings.TrimSpace(email), normalized, binding); err != nil {
		return ErrSessionStore
	}
	if err = tx.Commit(ctx); err != nil {
		return ErrSessionStore
	}
	if err = s.mail.Send(ctx, mailer.Message{To: strings.TrimSpace(email), Purpose: mailer.VerifyEmail, Code: code}); err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = s.pool.Exec(cleanup, "DELETE FROM vault.email_challenges WHERE token_hash=$1", digest)
		return mailer.ErrDelivery
	}
	return nil
}

// CompleteEmailRegistration atomically consumes the code and anonymous session,
// creates the identity, and rotates the browser session. No account is created
// before mailbox ownership is proved; matching email never links accounts.
func (s *SessionStore) CompleteEmailRegistration(ctx context.Context, token, csrf, peer, code string, password []byte) (string, Session, error) {
	if s.mail == nil {
		return "", Session{}, ErrEmailDisabled
	}
	if err := s.checkAnonymous(ctx, token, csrf); err != nil {
		return "", Session{}, err
	}
	// Code verification has a five-attempt per-peer budget, plus the peer/global caps.
	peerKey := sha256.Sum256([]byte(peer))
	if err := s.reserveLoginAttempt(ctx, peer, hex.EncodeToString(peerKey[:])); err != nil {
		return "", Session{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	binding, _ := sessionDigest(token)
	// Autocommit attempt before validating. Failed guesses survive rollback and
	// concurrent guesses serialize on the challenge row.
	var expected []byte
	if err := s.pool.QueryRow(ctx, "SELECT vault.claim_email_attempt($1)", binding).Scan(&expected); err != nil {
		return "", Session{}, ErrSessionStore
	}
	digest, err := verificationDigest(token, code)
	if err != nil || len(expected) != 32 || subtle.ConstantTimeCompare(expected, digest) != 1 {
		return "", Session{}, ErrEmailVerification
	}
	select {
	case s.hashSlots <- struct{}{}:
	case <-ctx.Done():
		return "", Session{}, ErrSessionStore
	}
	hash, err := HashPassword(password)
	<-s.hashSlots
	if err != nil {
		return "", Session{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", Session{}, ErrSessionStore
	}
	defer rollbackSession(tx)
	anon, _ := sessionDigest(token)
	result, err := tx.Exec(ctx, "UPDATE vault.sessions SET revoked_at=clock_timestamp() WHERE token_hash=$1 AND user_id IS NULL AND revoked_at IS NULL AND csrf_token=$2 AND expires_at>clock_timestamp() AND idle_expires_at>clock_timestamp()", anon, csrf)
	if err != nil {
		return "", Session{}, ErrSessionStore
	}
	if result.RowsAffected() != 1 {
		return "", Session{}, ErrLoginForbidden
	}
	var user *string
	if err = tx.QueryRow(ctx, "SELECT vault.complete_email_registration($1,$2)::text", digest, hash).Scan(&user); err != nil {
		return "", Session{}, ErrSessionStore
	}
	if user == nil {
		return "", Session{}, ErrEmailVerification
	}
	raw, state, err := s.createInTransaction(ctx, tx, *user)
	if err != nil {
		return "", Session{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", Session{}, ErrSessionStore
	}
	return raw, state, nil
}

type registrationContextKey struct{}
type registrationCapability struct {
	request  func(context.Context, string) error
	complete func(context.Context, string, []byte) (Session, error)
}
type registrationEnabledKey struct{}

func RegistrationEnabled(ctx context.Context) bool {
	enabled, _ := ctx.Value(registrationEnabledKey{}).(bool)
	return enabled
}
func RequestRegistrationFromContext(ctx context.Context, email string) error {
	c, ok := ctx.Value(registrationContextKey{}).(registrationCapability)
	if !ok {
		return ErrLoginForbidden
	}
	return c.request(ctx, email)
}
func CompleteRegistrationFromContext(ctx context.Context, code string, password []byte) (Session, error) {
	c, ok := ctx.Value(registrationContextKey{}).(registrationCapability)
	if !ok {
		return Session{}, ErrLoginForbidden
	}
	return c.complete(ctx, code, password)
}
