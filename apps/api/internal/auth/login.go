package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrLoginRejected = errors.New("login rejected")

// CreateAnonymous is an internal bootstrap primitive used by integration tests.
// Production bootstrap uses BeginSession to enforce the shared allocation budget.
func (s *SessionStore) CreateAnonymous(ctx context.Context) (string, Session, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	token, err := newSessionToken()
	if err != nil {
		return "", Session{}, err
	}
	csrf, err := newSessionToken()
	if err != nil {
		return "", Session{}, err
	}
	digest, _ := sessionDigest(token)
	var state Session
	err = s.pool.QueryRow(ctx, `WITH instant AS MATERIALIZED (SELECT clock_timestamp() AS t)
 INSERT INTO vault.sessions(token_hash,user_id,csrf_token,created_at,last_seen_at,idle_expires_at,expires_at)
 SELECT $1,NULL,$2,t,t,t+interval '10 minutes',t+interval '10 minutes' FROM instant
 RETURNING csrf_token,idle_expires_at,expires_at`, digest, csrf).Scan(&state.CSRFToken, &state.IdleExpiresAt, &state.ExpiresAt)
	if err != nil {
		return "", Session{}, ErrSessionStore
	}
	return token, state, nil
}

// LookupAnonymous does not extend the fixed pre-login lifetime. Authenticated
// LookupAndTouch never accepts anonymous tokens.
func (s *SessionStore) LookupAnonymous(ctx context.Context, token string) (Session, error) {
	digest, err := sessionDigest(token)
	if err != nil {
		return Session{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var state Session
	err = s.pool.QueryRow(ctx, `SELECT csrf_token,idle_expires_at,expires_at FROM vault.sessions
 WHERE token_hash=$1 AND user_id IS NULL AND revoked_at IS NULL
 AND expires_at>clock_timestamp() AND idle_expires_at>clock_timestamp()`, digest).Scan(&state.CSRFToken, &state.IdleExpiresAt, &state.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrInvalidSession
	}
	if err != nil {
		return Session{}, ErrSessionStore
	}
	return state, nil
}

// Login verifies credentials outside locks, then rechecks their security version
// under a user lock before consuming the anonymous session. Tokens are returned
// only after commit. Browser callers must use LoginBrowser to enforce throttling.
func (s *SessionStore) Login(ctx context.Context, anonymous, csrf, login string, password []byte) (string, Session, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	state, err := s.LookupAnonymous(ctx, anonymous)
	if errors.Is(err, ErrInvalidSession) {
		return "", Session{}, ErrLoginRejected
	}
	if err != nil {
		return "", Session{}, err
	}
	if len(csrf) != 43 || subtle.ConstantTimeCompare([]byte(csrf), []byte(state.CSRFToken)) != 1 {
		return "", Session{}, ErrLoginRejected
	}
	name, nameErr := normalizeIdentifier(login)
	var user, hash string
	var version int64
	err = s.pool.QueryRow(ctx, `SELECT u.id::text,c.password_hash,u.auth_version FROM vault.credentials c
 JOIN vault.users u ON u.id=c.user_id WHERE c.login_name=$1 AND u.disabled_at IS NULL
 UNION ALL SELECT u.id::text,i.password_hash,u.auth_version FROM vault.user_identities i JOIN vault.users u ON u.id=i.user_id WHERE i.provider='password' AND i.provider_subject=$1 AND u.email_normalized=$1 AND u.disabled_at IS NULL`, name).Scan(&user, &hash, &version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", Session{}, ErrSessionStore
	}
	found := err == nil && nameErr == nil
	if !found {
		hash = s.dummyHash
	}
	// Per-store semaphore bounds hashing memory. LoginBrowser separately enforces
	// shared request budgets before this internal credential-verification primitive.
	select {
	case s.hashSlots <- struct{}{}:
	case <-ctx.Done():
		return "", Session{}, ErrSessionStore
	}
	ok, verifyErr := VerifyPassword(password, hash)
	<-s.hashSlots
	if verifyErr != nil {
		return "", Session{}, ErrSessionStore
	}
	if !found || !ok {
		return "", Session{}, ErrLoginRejected
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return "", Session{}, ErrSessionStore
	}
	defer rollbackSession(tx)
	var currentVersion int64
	err = tx.QueryRow(ctx, "SELECT auth_version FROM vault.users WHERE id=$1 AND disabled_at IS NULL FOR UPDATE", user).Scan(&currentVersion)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && currentVersion != version {
		return "", Session{}, ErrLoginRejected
	}
	if err != nil {
		return "", Session{}, ErrSessionStore
	}
	var currentHash string
	if err = tx.QueryRow(ctx, "SELECT password_hash FROM vault.credentials WHERE user_id=$1 UNION ALL SELECT password_hash FROM vault.user_identities WHERE user_id=$1 AND provider='password'", user).Scan(&currentHash); err != nil {
		return "", Session{}, ErrSessionStore
	}
	if currentHash != hash {
		return "", Session{}, ErrLoginRejected
	}
	if err = s.checkMFALogin(ctx, tx, user); err != nil {
		return "", Session{}, err
	}
	digest, _ := sessionDigest(anonymous)
	var locked []byte
	if err = tx.QueryRow(ctx, "SELECT token_hash FROM vault.sessions WHERE token_hash=$1 FOR UPDATE", digest).Scan(&locked); errors.Is(err, pgx.ErrNoRows) {
		return "", Session{}, ErrLoginRejected
	} else if err != nil {
		return "", Session{}, ErrSessionStore
	}
	result, err := tx.Exec(ctx, `UPDATE vault.sessions SET revoked_at=clock_timestamp()
 WHERE token_hash=$1 AND user_id IS NULL AND revoked_at IS NULL AND csrf_token=$2
 AND expires_at>clock_timestamp() AND idle_expires_at>clock_timestamp()`, digest, csrf)
	if err != nil {
		return "", Session{}, ErrSessionStore
	}
	if result.RowsAffected() != 1 {
		return "", Session{}, ErrLoginRejected
	}
	token, state, err := s.createInTransaction(ctx, tx, user)
	if err != nil {
		return "", Session{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", Session{}, ErrSessionStore
	}
	return token, state, nil
}

func rollbackSession(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
