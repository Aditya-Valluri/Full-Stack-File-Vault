package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"full-stack-file-vault.local/api/internal/auth"
	"full-stack-file-vault.local/api/internal/demostore"
	"github.com/jackc/pgx/v5"
)

// setup runs at each demo startup. It never resets accounts or existing data.
func setup() error {
	c, err := loadCredentials()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "/app/migrate")
	command.Env = []string{"MIGRATE_DATABASE_URL=" + c.operator, "MIGRATE_CONFIGURE_ROLES=true", "RUNTIME_PASSWORD=" + c.runtimePassword, "GC_PASSWORD=" + c.gcPassword}
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err = command.Run(); err != nil {
		return errors.New("demo migrations failed; operator must own the database and be able to create restricted roles")
	}
	conn, err := pgx.Connect(ctx, c.operator)
	if err != nil {
		return errors.New("demo setup database unavailable")
	}
	defer conn.Close(context.Background())
	tx, err := conn.Begin(ctx)
	if err != nil {
		return errors.New("demo setup transaction unavailable")
	}
	defer tx.Rollback(context.Background())
	// Lock only demo bootstrap, never application transactions.
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(724154,1)"); err != nil {
		return errors.New("demo bootstrap lock unavailable")
	}
	if _, err = tx.Exec(ctx, demostore.Schema); err != nil {
		return errors.New("demo byte-storage schema setup failed")
	}
	// Refuse an in-place switch from disk storage: metadata without its bytes
	// would silently break downloads. Deploy the free demo to a fresh database.
	var missing bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM vault.blobs b LEFT JOIN vault_demo.objects o USING(storage_key) WHERE o.storage_key IS NULL)").Scan(&missing); err != nil || missing {
		return errors.New("demo database contains disk-backed blobs; use a fresh database")
	}
	for _, account := range []struct{ login, role, key string }{
		{"reviewer", "USER", "DEMO_REVIEWER_PASSWORD"},
		{"reviewer-admin", "ADMIN", "DEMO_ADMIN_PASSWORD"},
	} {
		password := []byte(os.Getenv(account.key))
		hash, err := auth.HashPassword(password)
		if err != nil {
			clear(password)
			return errors.New(account.key + " must be a generated password of at least 15 characters")
		}
		var previous, role string
		var disabled bool
		err = tx.QueryRow(ctx, "SELECT c.password_hash,u.role,u.disabled_at IS NOT NULL FROM vault.credentials c JOIN vault.users u ON u.id=c.user_id WHERE c.login_name=$1", account.login).Scan(&previous, &role, &disabled)
		if err == nil {
			matches, verifyErr := auth.VerifyPassword(password, previous)
			clear(password)
			if verifyErr != nil || !matches || role != account.role || disabled {
				return errors.New("existing demo account differs; inspect it with operator access instead of overwriting it")
			}
			continue
		}
		clear(password)
		if !errors.Is(err, pgx.ErrNoRows) {
			return errors.New("cannot inspect demo account")
		}
		var id string
		if err = tx.QueryRow(ctx, "INSERT INTO vault.users(role) VALUES($1) RETURNING id::text", account.role).Scan(&id); err != nil {
			return errors.New("cannot create demo identity")
		}
		if _, err = tx.Exec(ctx, "INSERT INTO vault.credentials(user_id,login_name,password_hash) VALUES($1,$2,$3)", id, account.login, hash); err != nil {
			return errors.New("cannot create demo credentials")
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return errors.New("demo account commit failed; inspect state before retry")
	}
	fmt.Println("Demo schema and reviewer accounts ready. Retrieve passwords only from Render's environment settings.")
	return nil
}
