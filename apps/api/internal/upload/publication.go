package upload

import (
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"math"
	"sort"
	"time"

	"file-vault.local/api/internal/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrQuotaExceeded = errors.New("storage quota exceeded")
	ErrPublication   = errors.New("file publication unavailable")
	ErrIntegrity     = errors.New("stored content integrity check failed")
)

// PublishedFile contains logical metadata only; no content hash or reuse signal.
type PublishedFile struct {
	ID           string
	Name         string
	SizeBytes    int64
	DetectedMIME string
	CreatedAt    time.Time
}

type Publisher struct {
	pool   *pgxpool.Pool
	store  PublicationStore
	logger *slog.Logger
}

func NewPublisher(pool *pgxpool.Pool, store PublicationStore, logger *slog.Logger) (*Publisher, error) {
	if pool == nil || store == nil || logger == nil {
		return nil, ErrInvalidInput
	}
	return &Publisher{pool: pool, store: store, logger: logger}, nil
}

type candidate struct {
	info     Info
	key      string
	prepared PreparedObject
	blobID   string
}

// Publish atomically charges and creates one logical file per staged input.
// The context must come from BrowserSecurity; there is no client owner parameter.
// Caller retains ownership of Staged handles. Prepared copies are always cleaned.
// Durable candidate records survive failed/ambiguous commits for later recovery.
func (p *Publisher) Publish(ctx context.Context, files []*Staged, retryKeys ...string) ([]PublishedFile, error) {
	var retryKey string
	if len(retryKeys) > 1 {
		return nil, ErrInvalidInput
	}
	if len(retryKeys) == 1 {
		retryKey = retryKeys[0]
		if !receiptKey.MatchString(retryKey) {
			return nil, ErrInvalidInput
		}
	}
	fingerprint, fingerprintErr := uploadFingerprint(files)
	if fingerprintErr != nil {
		return nil, fingerprintErr
	}
	if _, err := auth.RequireUser(ctx); err != nil {
		return nil, err
	}
	if len(files) < 1 || len(files) > 100 {
		return nil, ErrInvalidInput
	}
	candidates := map[[32]byte]*candidate{}
	var ordered []*candidate
	var total int64
	defer func() {
		for _, c := range ordered {
			if c.prepared != nil {
				if err := c.prepared.Close(); err != nil {
					p.logger.Error("prepared upload cleanup failed", "code", "UPLOAD_CLEANUP_FAILED")
				}
			}
		}
	}()
	for _, file := range files {
		if file == nil {
			return nil, ErrInvalidInput
		}
		info := file.Info()
		if info.SizeBytes < 0 || info.SizeBytes > math.MaxInt64-total {
			return nil, ErrInvalidInput
		}
		total += info.SizeBytes
		if previous := candidates[info.SHA256]; previous != nil {
			if previous.info.SizeBytes != info.SizeBytes {
				return nil, ErrIntegrity
			}
			continue
		}
		c := &candidate{info: info}
		candidates[info.SHA256] = c
		ordered = append(ordered, c)
		var err error
		c.prepared, err = p.store.Prepare(ctx, file)
		if err != nil {
			return nil, ErrStorage
		}
		c.key, err = randomStorageKey("blob-")
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(ordered, func(i, j int) bool { return string(ordered[i].info.SHA256[:]) < string(ordered[j].info.SHA256[:]) })
	// Commit intents independently before any final object can appear. No eager final
	// object deletion is safe after an ambiguous publication commit.
	if err := p.recordCandidates(ctx, ordered); err != nil {
		return nil, err
	}
	transactionCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := p.pool.BeginTx(transactionCtx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, ErrPublication
	}
	defer rollbackPublication(tx)
	if _, err = tx.Exec(transactionCtx, "SET LOCAL lock_timeout='3s'; SET LOCAL statement_timeout='5s'"); err != nil {
		return nil, ErrPublication
	}
	identity, err := auth.LockPublicationIdentity(transactionCtx, tx)
	if err != nil {
		return nil, err
	}
	if retryKey != "" {
		previous, found, receiptErr := loadReceipt(transactionCtx, tx, identity.UserID, retryKey, fingerprint)
		if receiptErr != nil {
			return nil, receiptErr
		}
		if found {
			return previous, nil
		}
	}
	result, err := tx.Exec(transactionCtx, `UPDATE vault.users SET used_bytes=used_bytes+$2
 WHERE id=$1 AND disabled_at IS NULL AND $2<=quota_bytes-used_bytes`, identity.UserID, total)
	if err != nil {
		return nil, ErrPublication
	}
	if result.RowsAffected() != 1 {
		return nil, ErrQuotaExceeded
	}
	// Collisions on the truncated advisory key only serialize unrelated digests.
	// Sort signed keys, not full hashes, to keep one global multi-lock order.
	uniqueLocks := map[int64]bool{}
	for _, c := range ordered {
		uniqueLocks[int64(binary.BigEndian.Uint64(c.info.SHA256[:8]))] = true
	}
	keys := make([]int64, 0, len(uniqueLocks))
	for key := range uniqueLocks {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, key := range keys {
		if _, err = tx.Exec(transactionCtx, "SELECT pg_advisory_xact_lock($1)", key); err != nil {
			return nil, ErrPublication
		}
	}
	for _, c := range ordered {
		if err = p.publishBlob(transactionCtx, tx, c); err != nil {
			return nil, err
		}
	}
	output := make([]PublishedFile, 0, len(files))
	for _, file := range files {
		info := file.Info()
		c := candidates[info.SHA256]
		item := PublishedFile{Name: info.Name, SizeBytes: info.SizeBytes, DetectedMIME: info.DetectedMIME}
		err = tx.QueryRow(transactionCtx, `INSERT INTO vault.files(owner_id,blob_id,original_name,declared_mime)
 VALUES($1,$2,$3,NULLIF($4,'')) RETURNING id::text,created_at`, identity.UserID, c.blobID, info.Name, info.DeclaredMIME).Scan(&item.ID, &item.CreatedAt)
		if err != nil {
			return nil, ErrPublication
		}
		output = append(output, item)
	}
	if retryKey != "" {
		if err = saveReceipt(transactionCtx, tx, identity.UserID, retryKey, fingerprint, output); err != nil {
			return nil, err
		}
	}
	// Expiry may have elapsed during a bounded storage operation. Mutable account
	// and revocation changes are serialized by the locks already held.
	if _, err = auth.LockPublicationIdentity(transactionCtx, tx); err != nil {
		return nil, err
	}
	if err = tx.Commit(transactionCtx); err != nil {
		return nil, ErrPublication
	}
	return output, nil
}

func (p *Publisher) recordCandidates(ctx context.Context, candidates []*candidate) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return ErrPublication
	}
	defer rollbackPublication(tx)
	for _, c := range candidates {
		if _, err = tx.Exec(ctx, "INSERT INTO vault.object_candidates(storage_key,sha256,size_bytes) VALUES($1,$2,$3)", c.key, c.info.SHA256[:], c.info.SizeBytes); err != nil {
			return ErrPublication
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return ErrPublication
	}
	return nil
}

func (p *Publisher) publishBlob(ctx context.Context, tx pgx.Tx, c *candidate) error {
	var key, state string
	var size int64
	err := tx.QueryRow(ctx, "SELECT id::text,storage_key,size_bytes,state FROM vault.blobs WHERE sha256=$1 FOR UPDATE", c.info.SHA256[:]).Scan(&c.blobID, &key, &size, &state)
	exists := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ErrPublication
	}
	if exists && size != c.info.SizeBytes {
		return ErrIntegrity
	}
	var candidateState string
	if err = tx.QueryRow(ctx, "SELECT state FROM vault.object_candidates WHERE storage_key=$1 FOR UPDATE", c.key).Scan(&candidateState); err != nil {
		return ErrPublication
	}
	if candidateState != "PENDING" {
		return ErrPublication
	}
	promote := !exists
	if exists {
		verifyErr := p.store.Verify(ctx, key, size)
		if state == "ACTIVE" && verifyErr != nil {
			return ErrIntegrity
		}
		if state == "GC_PENDING" {
			var referenced bool
			if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM vault.files WHERE blob_id=$1)", c.blobID).Scan(&referenced); err != nil {
				return ErrPublication
			}
			if referenced {
				return ErrIntegrity
			}
			if verifyErr != nil && !errors.Is(verifyErr, ErrObjectMissing) {
				return ErrIntegrity
			}
			promote = errors.Is(verifyErr, ErrObjectMissing)
		} else if state != "ACTIVE" {
			return ErrIntegrity
		}
	}
	intentState := "CLEANUP"
	if promote {
		if err = c.prepared.Promote(ctx, c.key); err != nil {
			return ErrStorage
		}
		intentState = "PUBLISHED"
		if !exists {
			err = tx.QueryRow(ctx, `INSERT INTO vault.blobs(sha256,size_bytes,storage_key,detected_mime)
 VALUES($1,$2,$3,$4) RETURNING id::text`, c.info.SHA256[:], c.info.SizeBytes, c.key, c.info.DetectedMIME).Scan(&c.blobID)
			if err != nil {
				return ErrPublication
			}
		} else {
			if _, err = tx.Exec(ctx, "UPDATE vault.blobs SET storage_key=$2,state='ACTIVE',gc_after=NULL WHERE id=$1", c.blobID, c.key); err != nil {
				return ErrPublication
			}
			// The old, unreferenced generation retains an exact-key cleanup record.
			if _, err = tx.Exec(ctx, `INSERT INTO vault.object_candidates(storage_key,sha256,size_bytes,state) VALUES($1,$2,$3,'CLEANUP')
 ON CONFLICT(storage_key) DO UPDATE SET state='CLEANUP'`, key, c.info.SHA256[:], size); err != nil {
				return ErrPublication
			}
		}
	} else if state == "GC_PENDING" {
		if _, err = tx.Exec(ctx, "UPDATE vault.blobs SET state='ACTIVE',gc_after=NULL WHERE id=$1", c.blobID); err != nil {
			return ErrPublication
		}
	}
	if _, err = tx.Exec(ctx, "UPDATE vault.object_candidates SET state=$2 WHERE storage_key=$1", c.key, intentState); err != nil {
		return ErrPublication
	}
	return nil
}

// Usage reports stored versus relational usage under the same user lock as writers.
// It does not silently repair drift. Operator repair remains an explicit action.
type Usage struct{ StoredBytes, ActualBytes, QuotaBytes int64 }

func (p *Publisher) Reconcile(ctx context.Context) (Usage, error) {
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
	err = tx.QueryRow(ctx, `SELECT used_bytes,quota_bytes,
 (SELECT COALESCE(SUM(b.size_bytes),0)::bigint FROM vault.files f JOIN vault.blobs b ON b.id=f.blob_id WHERE f.owner_id=u.id)
 FROM vault.users u WHERE u.id=$1`, identity.UserID).Scan(&usage.StoredBytes, &usage.QuotaBytes, &usage.ActualBytes)
	if err != nil {
		return Usage{}, ErrPublication
	}
	if err = tx.Commit(ctx); err != nil {
		return Usage{}, ErrPublication
	}
	return usage, nil
}

func rollbackPublication(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
