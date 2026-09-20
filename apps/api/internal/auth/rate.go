package auth

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RateLimitError exposes only a retry delay, never user identity or database state.
type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string { return "user request rate exceeded" }

// UserLimiter uses one row per user rather than a process-local allowance.
// All replicas must configure the same limit. Failed application operations still
// consume admission; rejected admissions do not extend the window.
type UserLimiter struct {
	pool  *pgxpool.Pool
	limit int
}

func NewUserLimiter(pool *pgxpool.Pool, limit int) (*UserLimiter, error) {
	if pool == nil || limit < 1 || limit > 100 {
		return nil, errors.New("user rate limit must be between 1 and 100")
	}
	return &UserLimiter{pool: pool, limit: limit}, nil
}

func (l *UserLimiter) Allow(ctx context.Context, userID string) error {
	if userID == "" {
		return ErrUnauthenticated
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	tx, err := l.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return ErrSessionStore
	}
	defer rollbackSession(tx)
	if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout='1s'; SET LOCAL statement_timeout='1500ms'"); err != nil {
		return ErrSessionStore
	}
	if _, err = tx.Exec(ctx, "INSERT INTO vault.user_request_windows(user_id) VALUES($1) ON CONFLICT DO NOTHING", userID); err != nil {
		return ErrSessionStore
	}
	var accepted []time.Time
	if err = tx.QueryRow(ctx, "SELECT accepted_at FROM vault.user_request_windows WHERE user_id=$1 FOR UPDATE", userID).Scan(&accepted); err != nil {
		return ErrSessionStore
	}
	var now time.Time
	// Sample AFTER acquiring the shared row lock. Clamp a backward wall-clock step
	// conservatively; independent API clocks never determine admission.
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return ErrSessionStore
	}
	if len(accepted) > 0 && now.Before(accepted[len(accepted)-1]) {
		now = accepted[len(accepted)-1]
	}
	fresh := make([]time.Time, 0, l.limit)
	for _, at := range accepted {
		if at.After(now.Add(-time.Second)) {
			fresh = append(fresh, at)
		}
	}
	if len(fresh) >= l.limit {
		return &RateLimitError{RetryAfter: fresh[len(fresh)-l.limit].Add(time.Second).Sub(now)}
	}
	fresh = append(fresh, now)
	if _, err = tx.Exec(ctx, "UPDATE vault.user_request_windows SET accepted_at=$2 WHERE user_id=$1", userID, fresh); err != nil {
		return ErrSessionStore
	}
	if err = tx.Commit(ctx); err != nil {
		return ErrSessionStore
	}
	return nil
}

// Wrap runs after browser authentication/CSRF and before parsing JSON or uploads.
// Public and anonymous auth requests have separate bootstrap/login budgets.
func (l *UserLimiter) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state, err := RequireUser(r.Context())
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		if err = l.Allow(r.Context(), state.UserID); err != nil {
			var limited *RateLimitError
			if errors.As(err, &limited) {
				w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(limited.RetryAfter.Seconds())))))
				browserReject(w, 429, "RATE_LIMITED", "user request rate exceeded")
			} else {
				browserReject(w, 503, "INTERNAL_ERROR", "request admission unavailable")
			}
			return
		}
		next.ServeHTTP(w, r)
	})
}
