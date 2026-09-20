package auth

import "context"

// BrowserBinding includes anonymous sessions validated by the browser boundary.
// It grants no user identity or file permission. Sharing must recheck it in SQL.
func BrowserBinding(ctx context.Context) (Session, []byte, error) {
	state, ok := BrowserSessionFromContext(ctx)
	digest, bound := ctx.Value(boundSessionDigestKey{}).([]byte)
	if !ok || !bound || len(digest) != 32 {
		return Session{}, nil, ErrUnauthenticated
	}
	return state, append([]byte(nil), digest...), nil
}
