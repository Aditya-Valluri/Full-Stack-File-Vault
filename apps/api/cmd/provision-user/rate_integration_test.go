//go:build integration

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"full-stack-file-vault.local/api/internal/auth"
	"github.com/jackc/pgx/v5"
)

func testUserRateLimit(t *testing.T, ctx context.Context, admin *pgx.Conn, dsn, storageDirectory string) {
	f := newPublicationFixture(t, ctx, admin, dsn, storageDirectory)
	_, id, _, _ := f.user(100)
	one, err := auth.NewUserLimiter(f.pool, 2)
	if err != nil {
		t.Fatal(err)
	}
	two, err := auth.NewUserLimiter(f.other, 2)
	if err != nil {
		t.Fatal(err)
	}
	limiters := []*auth.UserLimiter{one, two}
	start := make(chan struct{})
	results := make(chan error, 12)
	for i := range 12 {
		go func(i int) { <-start; results <- limiters[i%2].Allow(ctx, id) }(i)
	}
	close(start)
	accepted, denied := 0, 0
	for range 12 {
		err := <-results
		var limited *auth.RateLimitError
		if err == nil {
			accepted++
		} else if errors.As(err, &limited) && limited.RetryAfter > 0 {
			denied++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 2 || denied != 10 {
		t.Fatalf("cross-pool accepted=%d denied=%d", accepted, denied)
	}
	_, other, _, _ := f.user(100)
	if err := two.Allow(ctx, other); err != nil {
		t.Fatal("another user blocked", err)
	}
	// Advance the persisted fixture, avoiding sleep-dependent expiry assertions.
	if _, err := admin.Exec(ctx, "UPDATE vault.user_request_windows SET accepted_at=ARRAY[clock_timestamp()-interval '2 seconds'] WHERE user_id=$1", id); err != nil {
		t.Fatal(err)
	}
	if err := one.Allow(ctx, id); err != nil {
		t.Fatal("expired requests charged", err)
	}
	// A future entry models clock rollback: must fail closed rather than reset.
	if _, err := admin.Exec(ctx, "UPDATE vault.user_request_windows SET accepted_at=ARRAY[clock_timestamp()+interval '1 minute',clock_timestamp()+interval '1 minute'] WHERE user_id=$1", id); err != nil {
		t.Fatal(err)
	}
	var limited *auth.RateLimitError
	if err := one.Allow(ctx, id); !errors.As(err, &limited) || limited.RetryAfter > time.Second {
		t.Fatal("clock rollback admitted request", err)
	}
	f.other.Close()
	if err := two.Allow(ctx, id); !errors.Is(err, auth.ErrSessionStore) {
		t.Fatal("database failure did not fail closed")
	}
}
