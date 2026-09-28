//go:build integration

package main

import (
	"context"
	"errors"
	"full-stack-file-vault.local/api/internal/auth"
	"full-stack-file-vault.local/api/internal/mailer"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
	"testing"
	"time"
)

type failedContactMail struct{ calls int }

func (m *failedContactMail) Send(context.Context, mailer.Message) error {
	m.calls++
	return errors.New("private provider failure")
}
func testContact(t *testing.T, ctx context.Context, operator *pgx.Conn, dsn string) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store, err := auth.NewSessionStore(pool, 24*time.Hour, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	capture := &capturedRegistrationMail{}
	store.ConfigureMail(capture)
	raw, state, err := store.CreateAnonymous(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reset := func() {
		t.Helper()
		if _, err := operator.Exec(ctx, "TRUNCATE vault.login_attempts; UPDATE vault.login_budget SET attempts=0,window_start=date_trunc('minute',clock_timestamp())"); err != nil {
			t.Fatal(err)
		}
	}
	reset()
	if err := store.ContactAdministrator(ctx, raw, "wrong", "127.0.0.1", "Subject", "Body"); err != auth.ErrLoginForbidden {
		t.Fatal("CSRF accepted")
	}
	if err := store.ContactAdministrator(ctx, raw, state.CSRFToken, "127.0.0.1", strings.Repeat("x", 121), "Body"); err != auth.ErrContactInvalid {
		t.Fatal("oversized subject accepted")
	}
	if len(capture.messages) != 0 {
		t.Fatal("rejected requests sent mail")
	}
	for i := 0; i < 5; i++ {
		if err := store.ContactAdministrator(ctx, raw, state.CSRFToken, "127.0.0.1", "Help", "Synthetic body"); err != nil {
			t.Fatal(err)
		}
	}
	another, otherState, err := store.CreateAnonymous(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ContactAdministrator(ctx, another, otherState.CSRFToken, "127.0.0.2", "Help", "Body"); err != auth.ErrLoginLimited {
		t.Fatal("global contact cap bypassed")
	}
	if len(capture.messages) != 5 {
		t.Fatal("unexpected send count")
	}
	for _, m := range capture.messages {
		if m.To != mailer.AdministratorInbox || m.Purpose != mailer.ContactAdministrator {
			t.Fatal("wrong destination/purpose")
		}
	}
	reset()
	failed := &failedContactMail{}
	store.ConfigureMail(failed)
	if err := store.ContactAdministrator(ctx, raw, state.CSRFToken, "127.0.0.1", "Help", "Body"); err != mailer.ErrDelivery || failed.calls != 1 {
		t.Fatal("provider failure not sanitized or retried")
	}
	store.ConfigureMail(nil)
	if err := store.ContactAdministrator(ctx, raw, state.CSRFToken, "127.0.0.1", "Help", "Body"); err != auth.ErrEmailDisabled {
		t.Fatal("disabled delivery accepted")
	}
}
