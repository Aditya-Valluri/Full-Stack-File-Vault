package files

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrInvariant = errors.New("file accounting integrity failure")

// Delete removes only the owned logical file. Quota release, grant revocation
// (FK cascade), and final-reference GC scheduling commit together.
func (s *Store) Delete(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, identity, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if !validID(id) {
		return ErrNotFound
	}
	var blobID string
	var digest []byte
	var size int64
	err = tx.QueryRow(ctx, "SELECT b.id::text,b.sha256,b.size_bytes FROM vault.files f JOIN vault.blobs b ON b.id=f.blob_id WHERE f.id=$1 AND f.owner_id=$2", id, identity.UserID).Scan(&blobID, &digest, &size)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return ErrUnavailable
	}
	if len(digest) != 32 {
		return ErrInvariant
	}
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(binary.BigEndian.Uint64(digest[:8]))); err != nil {
		return ErrUnavailable
	}
	var state string
	if err = tx.QueryRow(ctx, "SELECT state FROM vault.blobs WHERE id=$1 FOR UPDATE", blobID).Scan(&state); err != nil {
		return ErrUnavailable
	}
	if state != "ACTIVE" {
		return ErrInvariant
	}
	updated, err := tx.Exec(ctx, "UPDATE vault.users SET used_bytes=used_bytes-$2 WHERE id=$1 AND used_bytes>=$2", identity.UserID, size)
	if err != nil {
		return ErrUnavailable
	}
	if updated.RowsAffected() != 1 {
		return ErrInvariant
	}
	deleted, err := tx.Exec(ctx, "DELETE FROM vault.files WHERE id=$1 AND owner_id=$2", id, identity.UserID)
	if err != nil {
		return ErrUnavailable
	}
	if deleted.RowsAffected() != 1 {
		return ErrNotFound
	}
	if _, err = tx.Exec(ctx, "UPDATE vault.blobs SET state='GC_PENDING',gc_after=clock_timestamp()+interval '1 minute' WHERE id=$1 AND NOT EXISTS(SELECT 1 FROM vault.files WHERE blob_id=$1)", blobID); err != nil {
		return ErrUnavailable
	}
	if err = tx.Commit(ctx); err != nil {
		return ErrUnavailable
	}
	return nil
}

type Statistics struct {
	FileCount, LogicalBytes, UniqueContentBytes, SavedBytes int64
	SavingsPercent                                          string
}

// Statistics describes only content the caller already owns, independent of
// whether any other owner references the same blobs. It is not disk-usage telemetry.
func (s *Store) Statistics(ctx context.Context) (Statistics, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, identity, err := s.begin(ctx)
	if err != nil {
		return Statistics{}, err
	}
	defer rollback(tx)
	var result Statistics
	err = tx.QueryRow(ctx, `WITH owned AS (SELECT b.id,b.size_bytes FROM vault.files f JOIN vault.blobs b ON b.id=f.blob_id WHERE f.owner_id=$1)
 SELECT (SELECT count(*) FROM owned),(SELECT COALESCE(sum(size_bytes),0)::bigint FROM owned),
 (SELECT COALESCE(sum(size_bytes),0)::bigint FROM (SELECT DISTINCT id,size_bytes FROM owned) unique_content)`, identity.UserID).Scan(&result.FileCount, &result.LogicalBytes, &result.UniqueContentBytes)
	if err != nil {
		return Statistics{}, ErrUnavailable
	}
	result.SavedBytes = result.LogicalBytes - result.UniqueContentBytes
	result.SavingsPercent = "0.00"
	if result.LogicalBytes > 0 {
		result.SavingsPercent = fmt.Sprintf("%.2f", 100*float64(result.SavedBytes)/float64(result.LogicalBytes))
	}
	if err = tx.Commit(ctx); err != nil {
		return Statistics{}, ErrUnavailable
	}
	return result, nil
}
