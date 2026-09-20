// migrate applies forward migrations using a file-mounted operator credential.
// Database error text and credentials are never printed or accepted in argv.
package main

import (
	"context"
	"errors"
	"file-vault.local/api/internal/secret"
	"fmt"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "migration failed; inspect database state with operator access")
		os.Exit(1)
	}
	fmt.Println("database migrations complete")
}
func run() error {
	dsn, err := secret.Read("MIGRATE_DATABASE_URL")
	if err != nil || dsn == "" {
		return errors.New("operator credential required")
	}
	parsed, err := url.Parse(dsn)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		return errors.New("invalid operator URL")
	}
	directory := os.Getenv("MIGRATIONS_DIR")
	if directory == "" {
		directory = "/migrations"
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	source := (&url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}).String()
	parsed.Scheme = "pgx5"
	migrations, err := migrate.New(source, parsed.String())
	if err != nil {
		return err
	}
	err = migrations.Up()
	sourceErr, dbErr := migrations.Close()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	if sourceErr != nil || dbErr != nil {
		return errors.New("migration close failed")
	}
	if os.Getenv("MIGRATE_CONFIGURE_ROLES") != "true" {
		return nil
	}
	runtimePassword, err := secret.Read("RUNTIME_PASSWORD")
	if err != nil {
		return err
	}
	gcPassword, err := secret.Read("GC_PASSWORD")
	if err != nil {
		return err
	}
	format := regexp.MustCompile(`^[a-f0-9]{64}$`)
	if !format.MatchString(runtimePassword) || !format.MatchString(gcPassword) {
		return errors.New("role passwords must be random 32-byte hex values")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, "SELECT set_config('vault.runtime_password',$1,true),set_config('vault.gc_password',$2,true)", runtimePassword, gcPassword); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DO $$ BEGIN
 EXECUTE format('ALTER ROLE vault_runtime LOGIN PASSWORD %L',current_setting('vault.runtime_password'));
 EXECUTE format('ALTER ROLE vault_gc LOGIN PASSWORD %L',current_setting('vault.gc_password'));
 END $$;`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
