package auth

import (
	"context"
	"errors"
)

var ErrUnauthenticated = errors.New("authenticated session required")

// RequireUser consumes only middleware-validated session metadata. It is suitable
// for current-user reads; ownership-sensitive writes must recheck in their transaction.
func RequireUser(ctx context.Context) (Session, error) {
	state, ok := BrowserSessionFromContext(ctx)
	if !ok || state.UserID == "" {
		return Session{}, ErrUnauthenticated
	}
	if state.Role != "USER" && state.Role != "ADMIN" {
		return Session{}, ErrSessionStore
	}
	return state, nil
}
