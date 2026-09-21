// provision-user creates accounts using operator DB credentials, never the API role.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"full-stack-file-vault.local/api/internal/auth"
	"full-stack-file-vault.local/api/internal/secret"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"io"
	"os"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	login := flag.String("login", "", "ASCII username")
	role := flag.String("role", "USER", "USER or ADMIN")
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("password must be supplied through stdin, not arguments")
	}
	name, err := auth.NormalizeLogin(*login)
	if err != nil {
		return err
	}
	if *role != "USER" && *role != "ADMIN" {
		return errors.New("role must be USER or ADMIN")
	}
	dsn, err := secret.Read("PROVISION_DATABASE_URL")
	if err != nil {
		return err
	}
	if dsn == "" {
		return errors.New("PROVISION_DATABASE_URL is required")
	}
	info, err := os.Stdin.Stat()
	if err != nil {
		return errors.New("cannot inspect password input")
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return errors.New("pipe exact password bytes to stdin; terminal input is refused to prevent echo")
	}
	password, err := io.ReadAll(io.LimitReader(os.Stdin, auth.MaxPasswordBytes+1))
	if err != nil {
		return errors.New("cannot read password")
	}
	defer clear(password)
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return errors.New("operator database connection failed")
	}
	defer func() {
		c, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_ = conn.Close(c)
	}()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return errors.New("cannot begin provisioning")
	}
	defer func() {
		c, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_ = tx.Rollback(c)
	}()
	var id string
	if err = tx.QueryRow(ctx, "INSERT INTO vault.users(role) VALUES ($1) RETURNING id", *role).Scan(&id); err != nil {
		return errors.New("cannot create user; check operator privileges and migration 3")
	}
	_, err = tx.Exec(ctx, "INSERT INTO vault.credentials(user_id,login_name,password_hash) VALUES ($1,$2,$3)", id, name, hash)
	if err != nil {
		var pgerr *pgconn.PgError
		if errors.As(err, &pgerr) && pgerr.Code == "23505" {
			return errors.New("login already exists; existing account unchanged")
		}
		return errors.New("cannot create credentials")
	}
	if err = tx.Commit(ctx); err != nil {
		return errors.New("commit failed; verify account state before retry")
	}
	fmt.Fprintln(os.Stdout, "Created user", id, "with role", *role)
	return nil
}
