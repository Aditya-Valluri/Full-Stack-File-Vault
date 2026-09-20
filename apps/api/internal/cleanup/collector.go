// Package cleanup retires unreferenced generations before touching their bytes.
package cleanup

import (
	"context"
	"encoding/binary"
	"errors"
	"file-vault.local/api/internal/telemetry"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrCleanup = errors.New("storage cleanup unavailable")

type Storage interface {
	Remove(context.Context, string) error
}
type Collector struct {
	pool      *pgxpool.Pool
	storage   Storage
	batch     int
	temporary interface {
		RunOnce(context.Context) (int, error)
	}
}
type Report struct{ Retired, Removed, GrantsPruned, TemporaryRemoved int }

func NewCollector(pool *pgxpool.Pool, storage Storage, batch int, temporary ...interface {
	RunOnce(context.Context) (int, error)
}) (*Collector, error) {
	if pool == nil || storage == nil || batch < 1 || batch > 100 {
		return nil, errors.New("cleanup requires dependencies and batch size 1..100")
	}
	if len(temporary) > 1 {
		return nil, errors.New("at most one temporary cleaner")
	}
	collector := &Collector{pool: pool, storage: storage, batch: batch}
	if len(temporary) == 1 {
		collector.temporary = temporary[0]
	}
	return collector, nil
}

// RunOnce is bounded and safe across workers. A failed or ambiguous database commit
// never authorizes filesystem removal. Each item retries independently.
func (c *Collector) RunOnce(ctx context.Context) (report Report, err error) {
	defer func() { telemetry.Default.Cleanup(report.Removed, report.TemporaryRemoved, err != nil) }()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rows, err := c.pool.Query(ctx, "SELECT id::text,sha256 FROM vault.blobs WHERE state='GC_PENDING' AND gc_after<=clock_timestamp() ORDER BY gc_after,id LIMIT $1", c.batch)
	if err != nil {
		return report, ErrCleanup
	}
	type pending struct {
		id     string
		digest []byte
	}
	var blobs []pending
	for rows.Next() {
		var item pending
		if err = rows.Scan(&item.id, &item.digest); err != nil {
			rows.Close()
			return report, ErrCleanup
		}
		blobs = append(blobs, item)
	}
	rows.Close()
	if rows.Err() != nil {
		return report, ErrCleanup
	}
	var failed bool
	for _, item := range blobs {
		retired, err := c.retire(ctx, item.id, item.digest)
		if err != nil {
			failed = true
		} else if retired {
			report.Retired++
		}
	}
	rows, err = c.pool.Query(ctx, `SELECT storage_key,sha256 FROM vault.object_candidates
 WHERE next_attempt_at<=clock_timestamp() AND (state IN ('CLEANUP','DELETING')
 OR (state='PENDING' AND created_at<=clock_timestamp()-interval '10 minutes'))
 ORDER BY next_attempt_at,storage_key LIMIT $1`, c.batch)
	if err != nil {
		return report, ErrCleanup
	}
	type candidate struct {
		key    string
		digest []byte
	}
	var candidates []candidate
	for rows.Next() {
		var item candidate
		if err = rows.Scan(&item.key, &item.digest); err != nil {
			rows.Close()
			return report, ErrCleanup
		}
		candidates = append(candidates, item)
	}
	rows.Close()
	if rows.Err() != nil {
		return report, ErrCleanup
	}
	for _, item := range candidates {
		allowed, err := c.fence(ctx, item.key, item.digest)
		if err != nil {
			failed = true
			continue
		}
		if !allowed {
			continue
		}
		// No database transaction remains open during physical I/O. The durable fence
		// and detached generation prevent any new publisher from adopting this key.
		removeErr := c.storage.Remove(ctx, item.key)
		delay := time.Hour
		if removeErr != nil {
			delay = 30 * time.Second
			failed = true
		} else {
			report.Removed++
		}
		// Retain tombstones, including after success. A writer that lost its transaction
		// might finish a late filesystem call; repeat exact-key cleanup catches that leak.
		if _, err = c.pool.Exec(ctx, "UPDATE vault.object_candidates SET next_attempt_at=clock_timestamp()+make_interval(secs=>$2) WHERE storage_key=$1 AND state='DELETING'", item.key, delay.Seconds()); err != nil {
			failed = true
		}
	}
	result, err := c.pool.Exec(ctx, "DELETE FROM vault.file_access WHERE token_hash IN (SELECT token_hash FROM vault.file_access WHERE expires_at<=clock_timestamp() ORDER BY expires_at LIMIT $1)", c.batch)
	if err != nil {
		failed = true
	} else {
		report.GrantsPruned = int(result.RowsAffected())
	}
	shared, sharedErr := c.pool.Exec(ctx, "DELETE FROM vault.shared_access WHERE token_hash IN (SELECT token_hash FROM vault.shared_access WHERE expires_at<=clock_timestamp() ORDER BY expires_at LIMIT $1)", c.batch)
	if sharedErr != nil {
		failed = true
	} else {
		report.GrantsPruned += int(shared.RowsAffected())
	}
	if c.temporary != nil {
		removed, err := c.temporary.RunOnce(ctx)
		report.TemporaryRemoved = removed
		if err != nil {
			failed = true
		}
	}
	if failed {
		return report, ErrCleanup
	}
	return report, nil
}

func (c *Collector) transaction(ctx context.Context, digest []byte) (pgx.Tx, bool, error) {
	if len(digest) != 32 {
		return nil, false, ErrCleanup
	}
	tx, err := c.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, false, ErrCleanup
	}
	if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout='1s'; SET LOCAL statement_timeout='2s'"); err != nil {
		rollback(tx)
		return nil, false, ErrCleanup
	}
	var acquired bool
	if err = tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock($1)", int64(binary.BigEndian.Uint64(digest[:8]))).Scan(&acquired); err != nil {
		rollback(tx)
		return nil, false, ErrCleanup
	}
	if !acquired {
		rollback(tx)
		return nil, false, nil
	}
	return tx, true, nil
}
func (c *Collector) retire(ctx context.Context, id string, digest []byte) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	tx, acquired, err := c.transaction(ctx, digest)
	if err != nil || !acquired {
		return false, err
	}
	defer rollback(tx)
	var key string
	var size int64
	err = tx.QueryRow(ctx, "SELECT storage_key,size_bytes FROM vault.blobs WHERE id=$1 AND state='GC_PENDING' AND gc_after<=clock_timestamp() FOR UPDATE", id).Scan(&key, &size)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, ErrCleanup
	}
	var referenced bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM vault.files WHERE blob_id=$1)", id).Scan(&referenced); err != nil {
		return false, ErrCleanup
	}
	if referenced {
		return false, ErrCleanup
	}
	if _, err = tx.Exec(ctx, `INSERT INTO vault.object_candidates(storage_key,sha256,size_bytes,state)
 VALUES($1,$2,$3,'DELETING') ON CONFLICT(storage_key) DO UPDATE SET state='DELETING',next_attempt_at=clock_timestamp()`, key, digest, size); err != nil {
		return false, ErrCleanup
	}
	if _, err = tx.Exec(ctx, "DELETE FROM vault.blobs WHERE id=$1", id); err != nil {
		return false, ErrCleanup
	}
	if err = tx.Commit(ctx); err != nil {
		return false, ErrCleanup
	}
	return true, nil
}

func (c *Collector) fence(ctx context.Context, key string, digest []byte) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	tx, acquired, err := c.transaction(ctx, digest)
	if err != nil || !acquired {
		return false, err
	}
	defer rollback(tx)
	// Follow publication's digest -> blob -> intent lock order. Any committed blob
	// referencing the key wins, including GC_PENDING: retire must detach it first.
	var id string
	err = tx.QueryRow(ctx, "SELECT id::text FROM vault.blobs WHERE storage_key=$1 FOR UPDATE", key).Scan(&id)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, ErrCleanup
	}
	var state string
	var due bool
	err = tx.QueryRow(ctx, `SELECT state,next_attempt_at<=clock_timestamp() AND
 (state IN ('CLEANUP','DELETING') OR (state='PENDING' AND created_at<=clock_timestamp()-interval '10 minutes'))
 FROM vault.object_candidates WHERE storage_key=$1 FOR UPDATE`, key).Scan(&state, &due)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, ErrCleanup
	}
	if !due {
		return false, nil
	}
	if _, err = tx.Exec(ctx, "UPDATE vault.object_candidates SET state='DELETING',next_attempt_at=clock_timestamp()+interval '30 seconds' WHERE storage_key=$1", key); err != nil {
		return false, ErrCleanup
	}
	if err = tx.Commit(ctx); err != nil {
		return false, ErrCleanup
	}
	return true, nil
}
func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
