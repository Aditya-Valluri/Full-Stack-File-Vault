// Package database manages the application's PostgreSQL connection pool.
package database

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	// Driver errors can contain connection details. Never return the raw DSN error.
	if err != nil {
		return nil, errors.New("invalid DATABASE_URL")
	}
	cfg.MaxConns = 10
	cfg.MinConns = 0
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnLifetimeJitter = 5 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.ConnConfig.RuntimeParams["application_name"] = "vault-api"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, errors.New("database pool initialization failed")
	}
	if err := Ready(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// Ready checks connectivity, foundation tables, and the runtime identity.
// It deliberately avoids reading user content or exposing database errors.
func Ready(ctx context.Context, pool *pgxpool.Pool) error {
	var role string
	if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&role); err != nil {
		return errors.New("database unavailable")
	}
	if role != "vault_runtime" {
		return errors.New("database connection must use vault_runtime")
	}
	_, err := pool.Exec(ctx, `SELECT u.id, b.id, f.id FROM vault.users u, vault.blobs b, vault.files f LIMIT 0`)
	if err != nil {
		return errors.New("foundation schema unavailable")
	}
	return nil
}
