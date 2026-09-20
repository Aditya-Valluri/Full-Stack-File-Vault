package auth

import (
	"context"
	"net/http"
	"time"
)

// SessionBinding is internal service data, never a GraphQL field or client argument.
func SessionBinding(ctx context.Context) ([]byte, error) {
	if _, err := RequireUser(ctx); err != nil {
		return nil, err
	}
	digest, ok := ctx.Value(boundSessionDigestKey{}).([]byte)
	if !ok || len(digest) != 32 {
		return nil, ErrUnauthenticated
	}
	return append([]byte(nil), digest...), nil
}

// WrapContent permits read-only GET/HEAD with a session cookie. Unlike GraphQL POST,
// browser navigation cannot supply a CSRF header; the separate unpredictable grant
// supplies intent, and the service binds it to this exact validated session.
func (b *BrowserSecurity) WrapContent(next http.Handler) http.Handler {
	return b.wrapContent(next, false)
}

// WrapSharedContent permits anonymous browser sessions only at the separately
// authorized sharing transport. Personal file access continues to require a user.
func (b *BrowserSecurity) WrapSharedContent(next http.Handler) http.Handler {
	return b.wrapContent(next, true)
}
func (b *BrowserSecurity) wrapContent(next http.Handler, allowAnonymous bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		if r.Method != "GET" && r.Method != "HEAD" {
			w.Header().Set("Allow", "GET, HEAD")
			browserReject(w, 405, "METHOD_NOT_ALLOWED", "use GET or HEAD")
			return
		}
		origins := r.Header.Values("Origin")
		sites := r.Header.Values("Sec-Fetch-Site")
		if len(origins) > 1 || (len(origins) == 1 && origins[0] != b.config.Origin) || len(sites) > 1 || (len(sites) == 1 && sites[0] != "same-origin" && sites[0] != "none") {
			browserReject(w, 403, "FORBIDDEN", "request context rejected")
			return
		}
		cookies := r.CookiesNamed(b.cookieName)
		if len(cookies) != 1 {
			browserReject(w, 401, "UNAUTHENTICATED", "authentication required")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		state, err := b.sessions.LookupBrowserSession(ctx, cookies[0].Value)
		if err == nil && state.UserID != "" {
			state, err = b.sessions.LookupAndTouch(ctx, cookies[0].Value)
		}
		cancel()
		if err != nil {
			b.sessionFailure(w, err)
			return
		}
		if state.UserID == "" && !allowAnonymous {
			browserReject(w, 401, "UNAUTHENTICATED", "authentication required")
			return
		}
		digest, err := sessionDigest(cookies[0].Value)
		if err != nil {
			b.sessionFailure(w, err)
			return
		}
		requestCtx := context.WithValue(r.Context(), browserSessionKey{}, state)
		requestCtx = context.WithValue(requestCtx, boundSessionDigestKey{}, digest)
		next.ServeHTTP(w, r.WithContext(requestCtx))
	})
}
