package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalidSession = errors.New("session invalid or expired")
	ErrSessionStore   = errors.New("session store unavailable")
)

// Session is internal server state, not a GraphQL response or log payload.
type Session struct {
	UserID        string
	Role          string
	CSRFToken     string
	ExpiresAt     time.Time
	IdleExpiresAt time.Time
}

// SessionStore manages anonymous and authenticated sessions. Create's caller must verify
// credentials before creation. It does not authorize browser requests by itself.
type SessionStore struct {
	pool           *pgxpool.Pool
	absolute, idle time.Duration
	dummyHash      string
	hashSlots      chan struct{}
}

func NewSessionStore(pool *pgxpool.Pool, absolute, idle time.Duration) (*SessionStore, error) {
	if pool == nil || idle < time.Second || absolute < idle || absolute > 30*24*time.Hour || idle%time.Second != 0 || absolute%time.Second != 0 {
		return nil, errors.New("session lifetimes require whole seconds: 1s <= idle <= absolute <= 30d")
	}
	dummy, err := newSessionToken()
	if err != nil {
		return nil, err
	}
	dummyHash, err := HashPassword([]byte(dummy))
	if err != nil {
		return nil, ErrSessionStore
	}
	return &SessionStore{pool: pool, absolute: absolute, idle: idle, dummyHash: dummyHash, hashSlots: make(chan struct{}, 2)}, nil
}

func newSessionToken() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", ErrSessionStore
	}
	return base64.RawURLEncoding.EncodeToString(bytes[:]), nil
}

func sessionDigest(token string) ([]byte, error) {
	if len(token) != 43 {
		return nil, ErrInvalidSession
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(raw) != 32 {
		return nil, ErrInvalidSession
	}
	hash := sha256.Sum256(raw)
	return hash[:], nil
}

// Create returns the bearer token once. Only its SHA-256 digest enters PostgreSQL.
func (s *SessionStore) Create(ctx context.Context, userID string) (string, Session, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return "", Session{}, ErrSessionStore
	}
	defer rollbackSession(tx)
	var version int64
	err = tx.QueryRow(ctx, "SELECT auth_version FROM vault.users WHERE id=$1 AND disabled_at IS NULL FOR UPDATE", userID).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", Session{}, ErrInvalidSession
	}
	if err != nil {
		return "", Session{}, ErrSessionStore
	}
	token, state, err := s.createInTransaction(ctx, tx, userID)
	if err != nil {
		return "", Session{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", Session{}, ErrSessionStore
	}
	return token, state, nil
}

// Caller holds the user row lock. Shared by trusted creation and atomic rotation.
func (s *SessionStore) createInTransaction(ctx context.Context, tx pgx.Tx, userID string) (string, Session, error) {
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
	err = tx.QueryRow(ctx, `WITH instant AS MATERIALIZED (SELECT clock_timestamp() AS t)
 INSERT INTO vault.sessions(token_hash,user_id,csrf_token,created_at,last_seen_at,idle_expires_at,expires_at)
 SELECT $1,u.id,$3,t,t,t+make_interval(secs => $4),t+make_interval(secs => $5)
 FROM vault.users u CROSS JOIN instant WHERE u.id=$2 AND u.disabled_at IS NULL
 RETURNING user_id::text,csrf_token,idle_expires_at,expires_at,
 (SELECT role FROM vault.users WHERE id=user_id)`, digest, userID, csrf, s.idle.Seconds(), s.absolute.Seconds()).Scan(&state.UserID, &state.CSRFToken, &state.IdleExpiresAt, &state.ExpiresAt, &state.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", Session{}, ErrInvalidSession
	}
	if err != nil {
		return "", Session{}, ErrSessionStore
	}
	return token, state, nil
}

// LookupAndTouch serializes with revocation and samples database time AFTER the
// row lock is obtained, so lock contention cannot validate against an old timestamp.
func (s *SessionStore) LookupAndTouch(ctx context.Context, token string) (Session, error) {
	digest, err := sessionDigest(token)
	if err != nil {
		return Session{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return Session{}, ErrSessionStore
	}
	defer func() {
		c, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		_ = tx.Rollback(c)
	}()
	var locked []byte
	err = tx.QueryRow(ctx, "SELECT token_hash FROM vault.sessions WHERE token_hash=$1 FOR UPDATE", digest).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrInvalidSession
	}
	if err != nil {
		return Session{}, ErrSessionStore
	}
	var state Session
	err = tx.QueryRow(ctx, `WITH instant AS MATERIALIZED (SELECT clock_timestamp() AS t)
 UPDATE vault.sessions s SET last_seen_at=t,
 idle_expires_at=LEAST(s.expires_at,t+make_interval(secs => $2))
 FROM instant,vault.users u WHERE s.token_hash=$1 AND u.id=s.user_id
 AND u.disabled_at IS NULL AND s.revoked_at IS NULL AND s.expires_at>t AND s.idle_expires_at>t
 RETURNING s.user_id::text,u.role,s.csrf_token,s.idle_expires_at,s.expires_at`, digest, s.idle.Seconds()).Scan(&state.UserID, &state.Role, &state.CSRFToken, &state.IdleExpiresAt, &state.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrInvalidSession
	}
	if err != nil {
		return Session{}, ErrSessionStore
	}
	if err = tx.Commit(ctx); err != nil {
		return Session{}, ErrSessionStore
	}
	return state, nil
}

// LookupBrowserSession reads valid anonymous or authenticated state without
// renewing it. It is used to reject invalid CSRF before any activity write.
func (s *SessionStore) LookupBrowserSession(ctx context.Context, token string) (Session, error) {
	digest, err := sessionDigest(token)
	if err != nil {
		return Session{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var state Session
	err = s.pool.QueryRow(ctx, `SELECT COALESCE(s.user_id::text,''),COALESCE(u.role,''),s.csrf_token,s.idle_expires_at,s.expires_at
 FROM vault.sessions s LEFT JOIN vault.users u ON u.id=s.user_id
 WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp()
 AND s.idle_expires_at>clock_timestamp() AND (s.user_id IS NULL OR u.disabled_at IS NULL)`, digest).Scan(&state.UserID, &state.Role, &state.CSRFToken, &state.IdleExpiresAt, &state.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrInvalidSession
	}
	if err != nil {
		return Session{}, ErrSessionStore
	}
	return state, nil
}

// Revoke is idempotent. A lookup already completed before revocation may finish;
// future sensitive writes must recheck authorization in their own transaction.
func (s *SessionStore) Revoke(ctx context.Context, token string) error {
	digest, err := sessionDigest(token)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err = s.pool.Exec(ctx, "UPDATE vault.sessions SET revoked_at=clock_timestamp() WHERE token_hash=$1 AND revoked_at IS NULL", digest)
	if err != nil {
		return ErrSessionStore
	}
	return nil
}

// RevokeUser increments the user's security version before revoking existing sessions.
// Credential verification started before this transaction cannot publish a session.
func (s *SessionStore) RevokeUser(ctx context.Context, userID string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return ErrSessionStore
	}
	defer rollbackSession(tx)
	result, err := tx.Exec(ctx, "UPDATE vault.users SET auth_version=auth_version+1 WHERE id=$1", userID)
	if err != nil {
		return ErrSessionStore
	}
	if result.RowsAffected() != 1 {
		return ErrInvalidSession
	}
	_, err = tx.Exec(ctx, "UPDATE vault.sessions SET revoked_at=clock_timestamp() WHERE user_id=$1 AND revoked_at IS NULL", userID)
	if err != nil {
		return ErrSessionStore
	}
	if err = tx.Commit(ctx); err != nil {
		return ErrSessionStore
	}
	return nil
}
