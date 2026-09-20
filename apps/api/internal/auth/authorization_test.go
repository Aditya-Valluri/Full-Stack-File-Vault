package auth

import (
	"context"
	"errors"
	"testing"
)

func TestRequireUser(t *testing.T) {
	if _, err := RequireUser(context.Background()); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("missing context accepted")
	}
	for _, tc := range []struct {
		state Session
		want  error
	}{
		{Session{CSRFToken: "anonymous"}, ErrUnauthenticated},
		{Session{UserID: "user", Role: "USER"}, nil},
		{Session{UserID: "admin", Role: "ADMIN"}, nil},
		{Session{UserID: "user", Role: "unexpected"}, ErrSessionStore},
	} {
		got, err := RequireUser(context.WithValue(context.Background(), browserSessionKey{}, tc.state))
		if !errors.Is(err, tc.want) {
			t.Fatalf("unexpected authorization error: %v", err)
		}
		if err == nil && (got.UserID != tc.state.UserID || got.Role != tc.state.Role) {
			t.Fatal("identity changed")
		}
	}
}
