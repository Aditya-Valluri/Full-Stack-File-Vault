package auth

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
)

type MFAResult struct {
	Available, Enabled bool
	URI                string
	RecoveryCodes      []string
}
type secondFactorKey struct{}

func WithSecondFactor(ctx context.Context, code string) context.Context {
	return context.WithValue(ctx, secondFactorKey{}, code)
}

type mfaContextKey struct{}
type mfaCapability func(context.Context, string, []byte, string) (MFAResult, error)

func MFAFromContext(ctx context.Context, action string, password []byte, code string) (MFAResult, error) {
	fn, ok := ctx.Value(mfaContextKey{}).(mfaCapability)
	if !ok {
		return MFAResult{}, ErrUnauthenticated
	}
	return fn(ctx, action, password, code)
}

// Caller has locked the user before checking/consuming its second factor.
func (s *SessionStore) checkMFALogin(ctx context.Context, tx pgx.Tx, user string) error {
	var encrypted []byte
	var last int64
	err := tx.QueryRow(ctx, "SELECT encrypted_secret,last_step FROM vault.user_mfa WHERE user_id=$1 AND enabled", user).Scan(&encrypted, &last)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return ErrSessionStore
	}
	code, _ := ctx.Value(secondFactorKey{}).(string)
	if code == "" {
		return ErrMFARequired
	}
	if err = s.consumeFactor(ctx, tx, user, encrypted, last, code); errors.Is(err, ErrMFARejected) {
		return ErrLoginRejected
	}
	return err
}
func (s *SessionStore) consumeFactor(ctx context.Context, tx pgx.Tx, user string, encrypted []byte, last int64, code string) error {
	if len(code) > 64 {
		return ErrMFARejected
	}
	if digest, ok := recoveryDigest(user, code); ok {
		// Recovery remains usable if a key is lost, but still requires the password.
		result, err := tx.Exec(ctx, "DELETE FROM vault.mfa_recovery_codes WHERE user_id=$1 AND code_hash=$2", user, digest)
		if err != nil {
			return ErrSessionStore
		}
		if result.RowsAffected() != 1 {
			return ErrMFARejected
		}
		return nil
	}
	seed, err := s.decryptMFA(user, encrypted)
	if err != nil {
		return err
	}
	defer clear(seed)
	var now time.Time
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return ErrSessionStore
	}
	step, ok := verifyTOTP(seed, code, now, last)
	if !ok {
		return ErrMFARejected
	}
	if _, err = tx.Exec(ctx, "UPDATE vault.user_mfa SET last_step=$2 WHERE user_id=$1", user, step); err != nil {
		return ErrSessionStore
	}
	return nil
}

// Verify the password outside database locks, then recheck the same hash under
// the user/session locks. Concurrent password changes cannot authorize enrollment.
func (s *SessionStore) mfaReauthenticate(ctx context.Context, password []byte) (pgx.Tx, Session, error) {
	identity, err := RequireUser(ctx)
	if err != nil {
		return nil, identity, err
	}
	const hashQuery = "SELECT password_hash FROM vault.credentials WHERE user_id=$1 UNION ALL SELECT password_hash FROM vault.user_identities WHERE user_id=$1 AND provider='password'"
	var hash string
	if err = s.pool.QueryRow(ctx, hashQuery, identity.UserID).Scan(&hash); err != nil {
		return nil, identity, ErrPasswordRejected
	}
	select {
	case s.hashSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, identity, ErrSessionStore
	}
	ok, err := VerifyPassword(password, hash)
	<-s.hashSlots
	if err != nil {
		return nil, identity, ErrSessionStore
	}
	if !ok {
		return nil, identity, ErrPasswordRejected
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, identity, ErrSessionStore
	}
	accepted := false
	defer func() {
		if !accepted {
			rollbackSession(tx)
		}
	}()
	if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout='2s'; SET LOCAL statement_timeout='3s'"); err != nil {
		return nil, identity, ErrSessionStore
	}
	var locked string
	if err = tx.QueryRow(ctx, "SELECT id::text FROM vault.users WHERE id=$1 FOR UPDATE", identity.UserID).Scan(&locked); err != nil {
		return nil, identity, ErrUnauthenticated
	}
	identity, err = LockPublicationIdentity(ctx, tx)
	if err != nil {
		return nil, identity, err
	}
	var current string
	if err = tx.QueryRow(ctx, hashQuery, identity.UserID).Scan(&current); err != nil {
		return nil, identity, ErrSessionStore
	}
	if current != hash {
		return nil, identity, ErrPasswordRejected
	}
	accepted = true
	return tx, identity, nil
}
func (s *SessionStore) MFA(ctx context.Context, peer, action string, password []byte, code string) (MFAResult, error) {
	identity, err := RequireUser(ctx)
	if err != nil {
		return MFAResult{}, err
	}
	result := MFAResult{Available: s.mfaCipher != nil, RecoveryCodes: []string{}}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if action == "status" {
		err = s.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM vault.user_mfa WHERE user_id=$1 AND enabled)", identity.UserID).Scan(&result.Enabled)
		if err != nil {
			return result, ErrSessionStore
		}
		return result, nil
	}
	if action != "disable" && s.mfaCipher == nil {
		return result, ErrMFAUnavailable
	}
	if err = s.reserveLoginAttempt(ctx, peer, identity.LoginName); err != nil {
		return result, err
	}
	tx, identity, err := s.mfaReauthenticate(ctx, password)
	if err != nil {
		return result, err
	}
	defer rollbackSession(tx)
	switch action {
	case "setup":
		var enabled bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM vault.user_mfa WHERE user_id=$1 AND enabled)", identity.UserID).Scan(&enabled); err != nil {
			return result, ErrSessionStore
		}
		if enabled {
			return result, ErrMFARejected
		}
		seed := make([]byte, 20)
		if _, err = rand.Read(seed); err != nil {
			return result, ErrSessionStore
		}
		defer clear(seed)
		encrypted, e := s.encryptMFA(identity.UserID, seed)
		if e != nil {
			return result, e
		}
		_, err = tx.Exec(ctx, `INSERT INTO vault.user_mfa(user_id,encrypted_secret) VALUES($1,$2)
 ON CONFLICT(user_id) DO UPDATE SET encrypted_secret=$2,setup_expires_at=clock_timestamp()+interval '10 minutes',last_step=-1 WHERE NOT vault.user_mfa.enabled`, identity.UserID, encrypted)
		if err != nil {
			return result, ErrSessionStore
		}
		values := url.Values{"secret": {base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(seed)}, "issuer": {"Full Stack File Vault"}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
		result.URI = "otpauth://totp/" + url.PathEscape("Full Stack File Vault:"+identity.LoginName) + "?" + values.Encode()
	case "enable", "disable":
		var encrypted []byte
		var enabled, unexpired bool
		var last int64
		err = tx.QueryRow(ctx, "SELECT encrypted_secret,enabled,setup_expires_at>clock_timestamp(),last_step FROM vault.user_mfa WHERE user_id=$1 FOR UPDATE", identity.UserID).Scan(&encrypted, &enabled, &unexpired, &last)
		if errors.Is(err, pgx.ErrNoRows) {
			return result, ErrMFARejected
		}
		if err != nil {
			return result, ErrSessionStore
		}
		if action == "enable" && (enabled || !unexpired) {
			return result, ErrMFARejected
		}
		if action == "disable" && !enabled {
			return result, ErrMFARejected
		}
		if action == "enable" && len(code) != 6 {
			return result, ErrMFARejected
		}
		if err = s.consumeFactor(ctx, tx, identity.UserID, encrypted, last, code); err != nil {
			return result, err
		}
		if action == "enable" {
			if _, err = tx.Exec(ctx, "UPDATE vault.user_mfa SET enabled=true WHERE user_id=$1", identity.UserID); err != nil {
				return result, ErrSessionStore
			}
			for i := 0; i < 8; i++ {
				var bytes [16]byte
				if _, err = rand.Read(bytes[:]); err != nil {
					return result, ErrSessionStore
				}
				code := hex.EncodeToString(bytes[:])
				digest, _ := recoveryDigest(identity.UserID, code)
				clear(bytes[:])
				if _, err = tx.Exec(ctx, "INSERT INTO vault.mfa_recovery_codes(user_id,code_hash) VALUES($1,$2)", identity.UserID, digest); err != nil {
					return result, ErrSessionStore
				}
				result.RecoveryCodes = append(result.RecoveryCodes, code)
			}
			result.Enabled = true
		} else {
			if _, err = tx.Exec(ctx, "DELETE FROM vault.user_mfa WHERE user_id=$1", identity.UserID); err != nil {
				return result, ErrSessionStore
			}
		}
		if _, err = tx.Exec(ctx, "UPDATE vault.users SET auth_version=auth_version+1 WHERE id=$1", identity.UserID); err != nil {
			return result, ErrSessionStore
		}
		if _, err = tx.Exec(ctx, "UPDATE vault.sessions SET revoked_at=clock_timestamp() WHERE user_id=$1 AND revoked_at IS NULL", identity.UserID); err != nil {
			return result, ErrSessionStore
		}
	default:
		return result, ErrMFARejected
	}
	if err = tx.Commit(ctx); err != nil {
		return MFAResult{}, ErrSessionStore
	}
	return result, nil
}
