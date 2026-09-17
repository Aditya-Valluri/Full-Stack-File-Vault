package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	ErrBootstrapLimited   = errors.New("session bootstrap temporarily limited")
	ErrBootstrapForbidden = errors.New("session bootstrap not authorized")
)

type BootstrapFunc func(context.Context, string) (string, Session, bool, error)
type bootstrapContextKey struct{}

// BootstrapFromContext invokes a capability installed only by validated browser
// bootstrap middleware. A header alone never grants this capability.
func BootstrapFromContext(ctx context.Context) (Session, error) {
	fn, ok := ctx.Value(bootstrapContextKey{}).(func(context.Context) (Session, error))
	if !ok {
		return Session{}, ErrBootstrapForbidden
	}
	return fn(ctx)
}

// BeginSession reuses valid state without renewal or identity changes. Allocation
// is globally budgeted in the same transaction as insertion, across API replicas.
func (s *SessionStore) BeginSession(ctx context.Context, existing string, limit int) (string, Session, bool, error) {
	if limit < 1 || limit > 600 {
		return "", Session{}, false, ErrSessionStore
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if existing != "" {
		state, err := s.LookupBrowserSession(ctx, existing)
		if err == nil {
			return existing, state, false, nil
		}
		if !errors.Is(err, ErrInvalidSession) {
			return "", Session{}, false, err
		}
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return "", Session{}, false, ErrSessionStore
	}
	defer rollbackSession(tx)
	var locked int
	if err = tx.QueryRow(ctx, "SELECT id FROM vault.bootstrap_budget WHERE id=1 FOR UPDATE").Scan(&locked); err != nil {
		return "", Session{}, false, ErrSessionStore
	}
	result, err := tx.Exec(ctx, `WITH instant AS MATERIALIZED (SELECT date_trunc('minute',clock_timestamp()) AS t)
 UPDATE vault.bootstrap_budget b SET window_start=GREATEST(b.window_start,t),
 creations=CASE WHEN b.window_start>=t THEN b.creations+1 ELSE 1 END
 FROM instant WHERE b.id=1 AND (b.window_start<t OR b.creations<$1)`, limit)
	if err != nil {
		return "", Session{}, false, ErrSessionStore
	}
	if result.RowsAffected() != 1 {
		return "", Session{}, false, ErrBootstrapLimited
	}
	token, err := newSessionToken()
	if err != nil {
		return "", Session{}, false, err
	}
	csrf, err := newSessionToken()
	if err != nil {
		return "", Session{}, false, err
	}
	digest, _ := sessionDigest(token)
	var state Session
	err = tx.QueryRow(ctx, `WITH instant AS MATERIALIZED (SELECT clock_timestamp() AS t)
 INSERT INTO vault.sessions(token_hash,user_id,csrf_token,created_at,last_seen_at,idle_expires_at,expires_at)
 SELECT $1,NULL,$2,t,t,t+interval '10 minutes',t+interval '10 minutes' FROM instant
 RETURNING csrf_token,idle_expires_at,expires_at`, digest, csrf).Scan(&state.CSRFToken, &state.IdleExpiresAt, &state.ExpiresAt)
	if err != nil {
		return "", Session{}, false, ErrSessionStore
	}
	if err = tx.Commit(ctx); err != nil {
		return "", Session{}, false, ErrSessionStore
	}
	return token, state, true, nil
}
