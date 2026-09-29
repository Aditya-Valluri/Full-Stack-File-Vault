package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
)

// PasswordBackoffError contains only a server-computed retry duration. Unknown
// identifiers use exactly the same state machine as existing accounts.
type PasswordBackoffError struct{ RetryAfterSeconds int }

func (e *PasswordBackoffError) Error() string { return "password attempts temporarily restricted" }
func (e *PasswordBackoffError) Unwrap() error { return ErrLoginLimited }

type passwordTicket struct {
	key        []byte
	generation time.Time
}

func passwordKey(login string) []byte {
	name, err := normalizeIdentifier(login)
	if err != nil {
		name = "\x00invalid-login"
	}
	digest := sha256.Sum256([]byte(name))
	return digest[:]
}

func backoffDuration(level int) time.Duration {
	switch level {
	case 1:
		return 5 * time.Minute
	case 2:
		return 15 * time.Minute
	case 3:
		return 30 * time.Minute
	default:
		return time.Hour
	}
}

// Admission and settlement use short row locks, never locks held during Argon2.
// At most five failed or outstanding attempts are admitted per generation.
// A crashed request's lease expires, so it cannot lock an identifier forever.
func (s *SessionStore) beginPasswordAttempt(ctx context.Context, login string) (passwordTicket, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticket := passwordTicket{key: passwordKey(login)}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ticket, ErrSessionStore
	}
	defer rollbackSession(tx)
	_, err = tx.Exec(ctx, `INSERT INTO vault.login_attempts(scope,key_hash,expires_at,attempts)
 VALUES('password',$1,clock_timestamp()+interval '24 hours',1) ON CONFLICT DO NOTHING`, ticket.key)
	if err != nil {
		return ticket, ErrSessionStore
	}
	var failures, pending, level int
	var blocked, lease *time.Time
	var now time.Time
	err = tx.QueryRow(ctx, `SELECT failures,pending,lockouts,blocked_until,lease_until,generation,clock_timestamp()
 FROM vault.login_attempts WHERE scope='password' AND key_hash=$1 FOR UPDATE`, ticket.key).
		Scan(&failures, &pending, &level, &blocked, &lease, &ticket.generation, &now)
	if err != nil {
		return ticket, ErrSessionStore
	}
	if blocked != nil && blocked.After(now) {
		return ticket, &PasswordBackoffError{int(math.Ceil(blocked.Sub(now).Seconds()))}
	}
	// Reset completed lockouts and abandoned in-flight reservations, but keep the
	// escalation level until success or 24 hours without an admitted attempt.
	if blocked != nil || pending > 0 && lease != nil && !lease.After(now) {
		pending = 0
		if blocked != nil {
			failures = 0
		}
		ticket.generation = now
	}
	if failures+pending >= 5 {
		return ticket, &PasswordBackoffError{1}
	}
	_, err = tx.Exec(ctx, `UPDATE vault.login_attempts SET failures=$2,pending=$3,blocked_until=NULL,
 generation=$4,lease_until=$5::timestamptz+interval '20 seconds',expires_at=$5::timestamptz+interval '24 hours'
 WHERE scope='password' AND key_hash=$1`, ticket.key, failures, pending+1, ticket.generation, now)
	if err != nil {
		return ticket, ErrSessionStore
	}
	if err = tx.Commit(ctx); err != nil {
		return ticket, ErrSessionStore
	}
	return ticket, nil
}

func (s *SessionStore) finishPasswordAttempt(ticket passwordTicket, outcome error) error {
	// Settlement survives browser cancellation. Infrastructure failures release
	// their reservation without being counted as bad passwords.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ErrSessionStore
	}
	defer rollbackSession(tx)
	var failures, level, pending int
	var generation, now time.Time
	err = tx.QueryRow(ctx, `SELECT failures,lockouts,pending,generation,clock_timestamp()
 FROM vault.login_attempts WHERE scope='password' AND key_hash=$1 FOR UPDATE`, ticket.key).
		Scan(&failures, &level, &pending, &generation, &now)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return ErrSessionStore
	}
	if !generation.Equal(ticket.generation) {
		return nil
	}
	var blocked *time.Time
	if outcome == nil {
		failures = 0
		level = 0
		pending = 0
		generation = now
	} else {
		if pending > 0 {
			pending--
		}
		if errors.Is(outcome, ErrLoginRejected) {
			failures++
		}
		if failures >= 5 {
			failures = 5
			level++
			if level > 4 {
				level = 4
			}
			deadline := now.Add(backoffDuration(level))
			blocked = &deadline
		}
	}
	_, err = tx.Exec(ctx, `UPDATE vault.login_attempts SET failures=$2,lockouts=$3,pending=$4,generation=$5,blocked_until=$6
 WHERE scope='password' AND key_hash=$1`, ticket.key, failures, level, pending, generation, blocked)
	if err != nil {
		return ErrSessionStore
	}
	if err = tx.Commit(ctx); err != nil {
		return ErrSessionStore
	}
	if blocked != nil {
		return &PasswordBackoffError{int(backoffDuration(level).Seconds())}
	}
	return nil
}
