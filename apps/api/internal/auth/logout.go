package auth

import "context"

// LogoutFunc revokes only the credential bound by the browser middleware.
type LogoutFunc func(context.Context, string) error
type logoutContextKey struct{}
type logoutCapability func(context.Context) error

// LogoutFromContext requires authenticated, CSRF-validated browser state. The
// capability owns cookie clearing so resolvers never receive raw credentials.
func LogoutFromContext(ctx context.Context) error {
	if _, err := RequireUser(ctx); err != nil {
		return err
	}
	fn, ok := ctx.Value(logoutContextKey{}).(logoutCapability)
	if !ok {
		return ErrUnauthenticated
	}
	return fn(ctx)
}
