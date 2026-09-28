package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	ErrLoginLimited   = errors.New("login temporarily limited")
	ErrLoginForbidden = errors.New("login requires anonymous browser session")
)

// LoginFunc receives only a socket-derived peer address, never forwarded headers.
type LoginFunc func(ctx context.Context, anonymous, csrf, peer, login string, password []byte) (string, Session, error)
type loginContextKey struct{}
type loginCapability func(context.Context, string, []byte) (Session, error)

func LoginFromContext(ctx context.Context, login string, password []byte) (Session, error) {
	fn, ok := ctx.Value(loginContextKey{}).(loginCapability)
	if !ok {
		return Session{}, ErrLoginForbidden
	}
	return fn(ctx, login, password)
}

// LoginBrowser charges shared attempt budgets before password verification. Charges
// commit independently so failed credentials, cancellation and successful login all
// count. The internal Login primitive still rechecks anonymous state and CSRF.
func (s *SessionStore) LoginBrowser(ctx context.Context, anonymous, csrf, peer, login string, password []byte) (string, Session, error) {
	if err := s.reserveLoginAttempt(ctx, peer, login); err != nil {
		return "", Session{}, err
	}
	return s.Login(ctx, anonymous, csrf, login, password)
}

// Global admission bounds new key creation to at most two rows per admitted attempt.
// Expired keys are pruned under the same lock; inactive rows cannot grow without new
// admissions. Raw usernames/IPs are not retained; hashes are pseudonymous, not secret.
func (s *SessionStore) reserveLoginAttempt(ctx context.Context, peer, login string) error {
	ip, err := netip.ParseAddr(peer)
	if err != nil {
		return ErrSessionStore
	}
	name, err := normalizeIdentifier(login)
	if err != nil {
		name = "\x00invalid-login"
	}
	peerHash := sha256.Sum256([]byte(ip.Unmap().String()))
	nameHash := sha256.Sum256([]byte(name))
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return ErrSessionStore
	}
	defer rollbackSession(tx)
	var id int
	if err = tx.QueryRow(ctx, "SELECT id FROM vault.login_budget WHERE id=1 FOR UPDATE").Scan(&id); err != nil {
		return ErrSessionStore
	}
	var now time.Time
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return ErrSessionStore
	}
	result, err := tx.Exec(ctx, `UPDATE vault.login_budget SET
 window_start=GREATEST(window_start,date_trunc('minute',$1::timestamptz)),
 attempts=CASE WHEN window_start>=date_trunc('minute',$1::timestamptz) THEN attempts+1 ELSE 1 END
 WHERE id=1 AND (window_start<date_trunc('minute',$1::timestamptz) OR attempts<60)`, now)
	if err != nil {
		return ErrSessionStore
	}
	if result.RowsAffected() != 1 {
		return ErrLoginLimited
	}
	if _, err = tx.Exec(ctx, "DELETE FROM vault.login_attempts WHERE expires_at<=$1", now); err != nil {
		return ErrSessionStore
	}
	limited := false
	for _, budget := range []struct {
		scope  string
		key    []byte
		limit  int
		window time.Duration
	}{
		{"peer", peerHash[:], 20, time.Minute}, {"identifier", nameHash[:], 5, 15 * time.Minute},
	} {
		result, err = tx.Exec(ctx, `INSERT INTO vault.login_attempts(scope,key_hash,expires_at,attempts) VALUES($1,$2,$3,1)
 ON CONFLICT(scope,key_hash) DO UPDATE SET attempts=vault.login_attempts.attempts+1
 WHERE vault.login_attempts.attempts<$4`, budget.scope, budget.key, now.Add(budget.window), budget.limit)
		if err != nil {
			return ErrSessionStore
		}
		if result.RowsAffected() != 1 {
			limited = true
			break
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return ErrSessionStore
	}
	if limited {
		return ErrLoginLimited
	}
	return nil
}
