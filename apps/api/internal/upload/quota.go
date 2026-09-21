package upload

import (
	"context"
	"time"

	"full-stack-file-vault.local/api/internal/auth"
	"github.com/jackc/pgx/v5"
)

// Quota reads only the authoritative transactional counter; Reconcile is the
// separate diagnostic operation that compares it with the full relational sum.
func (p *Publisher) Quota(ctx context.Context) (Usage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return Usage{}, ErrPublication
	}
	defer rollbackPublication(tx)
	identity, err := auth.LockPublicationIdentity(ctx, tx)
	if err != nil {
		return Usage{}, err
	}
	var usage Usage
	if err = tx.QueryRow(ctx, "SELECT used_bytes,quota_bytes FROM vault.users WHERE id=$1", identity.UserID).Scan(&usage.StoredBytes, &usage.QuotaBytes); err != nil {
		return Usage{}, ErrPublication
	}
	if err = tx.Commit(ctx); err != nil {
		return Usage{}, ErrPublication
	}
	return usage, nil
}
