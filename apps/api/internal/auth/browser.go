package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type BrowserConfig struct {
	Origin      string
	Development bool
}

// ValidateBrowserConfig validates deployment configuration, never request headers.
func ValidateBrowserConfig(c BrowserConfig) error {
	u, err := url.Parse(c.Origin)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || strings.ContainsAny(u.Host, "*% ") {
		return errors.New("PUBLIC_ORIGIN must be an exact origin without path, credentials, query or fragment")
	}
	if u.Port() != "" {
		p, err := strconv.Atoi(u.Port())
		if err != nil || p < 1 || p > 65535 {
			return errors.New("invalid PUBLIC_ORIGIN port")
		}
	}
	if c.Origin != u.Scheme+"://"+u.Host || strings.HasSuffix(u.Host, ":") {
		return errors.New("PUBLIC_ORIGIN must use canonical origin syntax")
	}
	if u.Scheme == "https" {
		return nil
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if u.Scheme == "http" && c.Development && (host == "localhost" || ip != nil && ip.IsLoopback()) {
		return nil
	}
	return errors.New("PUBLIC_ORIGIN requires HTTPS except explicit loopback development")
}

// BrowserSessions is the storage boundary needed for CSRF validation. Reading
// first avoids extending session lifetime on rejected requests.
type BrowserSessions interface {
	LookupBrowserSession(context.Context, string) (Session, error)
	LookupAndTouch(context.Context, string) (Session, error)
}

type BrowserSecurity struct {
	config     BrowserConfig
	sessions   BrowserSessions
	cookieName string
	secure     bool
}

func NewBrowserSecurity(c BrowserConfig, sessions BrowserSessions) (*BrowserSecurity, error) {
	if err := ValidateBrowserConfig(c); err != nil {
		return nil, err
	}
	if sessions == nil {
		return nil, errors.New("browser session store is required")
	}
	secure := strings.HasPrefix(c.Origin, "https://")
	name := "__Host-vault_session"
	if !secure {
		name = "vault_session_dev"
	}
	return &BrowserSecurity{config: c, sessions: sessions, cookieName: name, secure: secure}, nil
}

type browserSessionKey struct{}

// BrowserSessionFromContext returns validated session metadata, including anonymous
// state with empty UserID. Protected resolvers must require a nonempty UserID.
func BrowserSessionFromContext(ctx context.Context) (Session, bool) {
	s, ok := ctx.Value(browserSessionKey{}).(Session)
	return s, ok
}

func (b *BrowserSecurity) SetSessionCookie(w http.ResponseWriter, token string, expires time.Time) error {
	if _, err := sessionDigest(token); err != nil {
		return err
	}
	if !expires.After(time.Now()) {
		return ErrInvalidSession
	}
	http.SetCookie(w, &http.Cookie{Name: b.cookieName, Value: token, Path: "/", Secure: b.secure, HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: expires.UTC()})
	return nil
}

func (b *BrowserSecurity) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: b.cookieName, Path: "/", Secure: b.secure, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0).UTC()})
}

// Wrap protects the entire GraphQL transport before body parsing. It does not
// authenticate cookie-less requests: only the current public schema is reachable.
// Use WrapBootstrap when exposing the operation-aware bootstrap mutation.
func (b *BrowserSecurity) Wrap(next http.Handler) http.Handler {
	return b.wrap(next, nil, nil, nil, nil)
}

// WrapBootstrap allows only a schema-validated, single-root bootstrap operation
// through the synchronizer-token exception; Origin/Fetch Metadata checks remain.
func (b *BrowserSecurity) WrapBootstrap(next http.Handler, validate func(*http.Request) error, begin BootstrapFunc) http.Handler {
	if validate == nil || begin == nil {
		panic("bootstrap dependencies required")
	}
	return b.wrap(next, validate, begin, nil, nil)
}

// WrapAuthentication installs identity-specific capabilities only after cookie/CSRF
// checks: anonymous state may log in, authenticated state may log out.
func (b *BrowserSecurity) WrapAuthentication(next http.Handler, validate func(*http.Request) error, begin BootstrapFunc, login LoginFunc, logout LogoutFunc) http.Handler {
	if validate == nil || begin == nil || login == nil || logout == nil {
		panic("authentication dependencies required")
	}
	return b.wrap(next, validate, begin, login, logout)
}

func (b *BrowserSecurity) wrap(next http.Handler, validate func(*http.Request) error, begin BootstrapFunc, login LoginFunc, logout LogoutFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if store, ok := b.sessions.(*SessionStore); ok {
			r = r.WithContext(context.WithValue(r.Context(), registrationEnabledKey{}, store.EmailRegistrationEnabled()))
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			browserReject(w, 405, "METHOD_NOT_ALLOWED", "use POST")
			return
		}
		origins := r.Header.Values("Origin")
		if len(origins) != 1 || origins[0] != b.config.Origin {
			browserReject(w, 403, "FORBIDDEN", "origin rejected")
			return
		}
		sites := r.Header.Values("Sec-Fetch-Site")
		if len(sites) > 1 || (len(sites) == 1 && sites[0] != "same-origin" && sites[0] != "none") {
			browserReject(w, 403, "FORBIDDEN", "request context rejected")
			return
		}
		// Exact Origin still applies when Fetch Metadata is absent. Do not reflect CORS
		// headers, or use Host/Forwarded to choose the trusted origin.
		var token string
		count := 0
		for _, cookie := range r.Cookies() {
			if cookie.Name == b.cookieName {
				count++
				token = cookie.Value
			}
		}
		if count > 1 {
			browserReject(w, 401, "UNAUTHENTICATED", "invalid session")
			return
		}
		bootstrapHeaders := r.Header.Values("X-Vault-CSRF-Bootstrap")
		if validate != nil && len(bootstrapHeaders) > 0 {
			if len(bootstrapHeaders) != 1 || bootstrapHeaders[0] != "1" {
				browserReject(w, 403, "FORBIDDEN", "bootstrap rejected")
				return
			}
			if err := validate(r); err != nil {
				browserReject(w, 400, "INVALID_INPUT", "bootstrap requires one valid beginSession mutation")
				return
			}
			capability := func(ctx context.Context) (Session, error) {
				raw, state, created, err := begin(ctx, token)
				if err != nil {
					return Session{}, err
				}
				if created {
					if err = b.SetSessionCookie(w, raw, state.ExpiresAt); err != nil {
						return Session{}, err
					}
				}
				return state, nil
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), bootstrapContextKey{}, capability)))
			return
		}
		if count == 0 {
			next.ServeHTTP(w, r)
			return
		}
		if count != 1 {
			browserReject(w, 401, "UNAUTHENTICATED", "invalid session")
			return
		}
		csrf := r.Header.Values("X-CSRF-Token")
		if len(csrf) != 1 || len(csrf[0]) != 43 {
			browserReject(w, 403, "FORBIDDEN", "CSRF validation failed")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		state, err := b.sessions.LookupBrowserSession(ctx, token)
		if err != nil {
			b.sessionFailure(w, err)
			return
		}
		if subtle.ConstantTimeCompare([]byte(csrf[0]), []byte(state.CSRFToken)) != 1 {
			browserReject(w, 403, "FORBIDDEN", "CSRF validation failed")
			return
		}
		if state.UserID != "" {
			state, err = b.sessions.LookupAndTouch(ctx, token)
			if err != nil {
				b.sessionFailure(w, err)
				return
			}
		}
		// The short storage deadline must not shorten future upload execution. Preserve
		// the original request context, carrying only the validated session snapshot.
		requestCtx := context.WithValue(r.Context(), browserSessionKey{}, state)
		{
			digest, err := sessionDigest(token)
			if err != nil {
				b.sessionFailure(w, err)
				return
			}
			requestCtx = context.WithValue(requestCtx, boundSessionDigestKey{}, digest)
		}
		if login != nil && state.UserID == "" {
			peer, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				browserReject(w, 503, "INTERNAL_ERROR", "session service unavailable")
				return
			}
			capability := loginCapability(func(ctx context.Context, name string, password []byte) (Session, error) {
				raw, authenticated, err := login(ctx, token, csrf[0], peer, name, password)
				if err != nil {
					return Session{}, err
				}
				if err = b.SetSessionCookie(w, raw, authenticated.ExpiresAt); err != nil {
					return Session{}, err
				}
				return authenticated, nil
			})
			requestCtx = context.WithValue(requestCtx, loginContextKey{}, capability)
			if store, ok := b.sessions.(*SessionStore); ok && store.EmailRegistrationEnabled() {
				requestCtx = context.WithValue(requestCtx, registrationContextKey{}, registrationCapability{
					request: func(ctx context.Context, email string) error {
						return store.RequestEmailRegistration(ctx, token, csrf[0], peer, email)
					},
					complete: func(ctx context.Context, code string, password []byte) (Session, error) {
						raw, state, err := store.CompleteEmailRegistration(ctx, token, csrf[0], peer, code, password)
						if err != nil {
							return Session{}, err
						}
						if err = b.SetSessionCookie(w, raw, state.ExpiresAt); err != nil {
							return Session{}, err
						}
						return state, nil
					},
				})
			}
		}
		if logout != nil && state.UserID != "" {
			capability := logoutCapability(func(ctx context.Context) error {
				if err := logout(ctx, token); err != nil {
					return err
				}
				b.ClearSessionCookie(w)
				return nil
			})
			requestCtx = context.WithValue(requestCtx, logoutContextKey{}, capability)
		}
		if store, ok := b.sessions.(*SessionStore); ok && login != nil {
			peer, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				browserReject(w, 503, "INTERNAL_ERROR", "session service unavailable")
				return
			}
			capability := passwordCapability{}
			if state.UserID == "" && store.EmailRegistrationEnabled() {
				requestCtx = context.WithValue(requestCtx, contactContextKey{}, contactCapability(func(ctx context.Context, subject, message string) error {
					return store.ContactAdministrator(ctx, token, csrf[0], peer, subject, message)
				}))
				capability.request = func(ctx context.Context, email string) error {
					return store.RequestPasswordReset(ctx, token, csrf[0], peer, email)
				}
				capability.complete = func(ctx context.Context, code string, password []byte) error {
					if err := store.CompletePasswordReset(ctx, token, csrf[0], peer, code, password); err != nil {
						return err
					}
					b.ClearSessionCookie(w)
					return nil
				}
			} else if state.UserID != "" {
				requestCtx = context.WithValue(requestCtx, mfaContextKey{}, mfaCapability(func(ctx context.Context, action string, password []byte, code string) (MFAResult, error) {
					result, err := store.MFA(ctx, peer, action, password, code)
					if err == nil && (action == "enable" || action == "disable") {
						b.ClearSessionCookie(w)
					}
					return result, err
				}))
				capability.change = func(ctx context.Context, current, next []byte) error {
					if err := store.ChangePassword(ctx, token, csrf[0], peer, current, next); err != nil {
						return err
					}
					b.ClearSessionCookie(w)
					return nil
				}
			}
			requestCtx = context.WithValue(requestCtx, passwordContextKey{}, capability)
		}
		next.ServeHTTP(w, r.WithContext(requestCtx))
	})
}

func (b *BrowserSecurity) sessionFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrInvalidSession) {
		b.ClearSessionCookie(w)
		browserReject(w, 401, "UNAUTHENTICATED", "invalid session")
		return
	}
	browserReject(w, 503, "INTERNAL_ERROR", "session service unavailable")
}

func browserReject(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"errors": []any{map[string]any{"message": message, "extensions": map[string]string{"code": code}}}})
}
