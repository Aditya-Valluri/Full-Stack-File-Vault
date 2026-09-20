package auth

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

type boundSessionDigestKey struct{}

// LockPublicationIdentity follows the global user-then-session lock order and
// rechecks mutable authorization after acquiring locks. Call within READ COMMITTED.
// It deliberately accepts no client-supplied owner ID or session hash.
func LockPublicationIdentity(ctx context.Context, tx pgx.Tx) (Session, error) {
	state, err := RequireUser(ctx)
	if err != nil {
		return Session{}, err
	}
	digest, ok := ctx.Value(boundSessionDigestKey{}).([]byte)
	if !ok || len(digest) != 32 {
		return Session{}, ErrUnauthenticated
	}
	var id string
	err = tx.QueryRow(ctx, "SELECT id::text FROM vault.users WHERE id=$1 FOR UPDATE", state.UserID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrUnauthenticated
	}
	if err != nil {
		return Session{}, ErrSessionStore
	}
	var locked []byte
	err = tx.QueryRow(ctx, "SELECT token_hash FROM vault.sessions WHERE token_hash=$1 FOR UPDATE", digest).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrUnauthenticated
	}
	if err != nil {
		return Session{}, ErrSessionStore
	}
	err = tx.QueryRow(ctx, `SELECT u.id::text,u.role FROM vault.users u JOIN vault.sessions s ON s.user_id=u.id
 WHERE u.id=$1 AND s.token_hash=$2 AND u.disabled_at IS NULL AND s.revoked_at IS NULL
 AND s.expires_at>clock_timestamp() AND s.idle_expires_at>clock_timestamp()`, state.UserID, digest).Scan(&state.UserID, &state.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrUnauthenticated
	}
	if err != nil {
		return Session{}, ErrSessionStore
	}
	return state, nil
}
