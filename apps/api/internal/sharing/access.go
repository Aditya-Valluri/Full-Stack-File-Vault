package sharing

import (
	"context"
	"errors"
	"time"

	"file-vault.local/api/internal/files"
	"github.com/jackc/pgx/v5"
)

func (s *Store) Inspect(ctx context.Context, token string) (SharedFile, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	state, binding, err := s.admit(ctx)
	if err != nil {
		return SharedFile{}, err
	}
	digest, err := tokenDigest(token)
	if err != nil {
		return SharedFile{}, err
	}
	tx, c, err := s.lockCapability(ctx, digest, binding, state, false)
	if err != nil {
		return SharedFile{}, err
	}
	defer rollback(tx)
	result := SharedFile{Name: c.file.Name, DetectedMIME: c.file.DetectedMIME, SizeBytes: c.file.SizeBytes, ExpiresAt: c.share.ExpiresAt,
		PreviewAllowed: c.share.Permission == "PREVIEW_AND_DOWNLOAD" && files.PreviewAllowed(c.file.DetectedMIME)}
	if err = tx.Commit(ctx); err != nil {
		return SharedFile{}, files.ErrUnavailable
	}
	return result, nil
}
func (s *Store) CreateAccess(ctx context.Context, token, mode string) (files.AccessGrant, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	state, binding, err := s.admit(ctx)
	if err != nil {
		return files.AccessGrant{}, err
	}
	digest, err := tokenDigest(token)
	if err != nil {
		return files.AccessGrant{}, err
	}
	if mode != "DOWNLOAD" && mode != "PREVIEW" {
		return files.AccessGrant{}, files.ErrInvalidInput
	}
	tx, c, err := s.lockCapability(ctx, digest, binding, state, false)
	if err != nil {
		return files.AccessGrant{}, err
	}
	defer rollback(tx)
	if mode == "PREVIEW" && (c.share.Permission != "PREVIEW_AND_DOWNLOAD" || !files.PreviewAllowed(c.file.DetectedMIME)) {
		return files.AccessGrant{}, files.ErrPreviewUnsupported
	}
	if _, err = tx.Exec(ctx, "DELETE FROM vault.shared_access WHERE session_hash=$1 AND expires_at<=clock_timestamp()", binding); err != nil {
		return files.AccessGrant{}, files.ErrUnavailable
	}
	var count int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM vault.shared_access WHERE session_hash=$1", binding).Scan(&count); err != nil {
		return files.AccessGrant{}, files.ErrUnavailable
	}
	if count >= 64 {
		return files.AccessGrant{}, ErrLimit
	}
	raw, grantHash, err := newToken()
	if err != nil {
		return files.AccessGrant{}, err
	}
	result := files.AccessGrant{URL: "/shared-content/" + raw}
	err = tx.QueryRow(ctx, `WITH instant AS MATERIALIZED(SELECT clock_timestamp() AS t)
 INSERT INTO vault.shared_access(token_hash,share_id,session_hash,mode,created_at,expires_at)
 SELECT $1,$2,$3,$4,t,LEAST(t+interval '1 minute',$5::timestamptz,$6::timestamptz) FROM instant
 RETURNING expires_at`, grantHash, c.share.ID, binding, mode, c.share.ExpiresAt, c.sessionDeadline).Scan(&result.ExpiresAt)
	if err != nil {
		return files.AccessGrant{}, files.ErrUnavailable
	}
	if err = tx.Commit(ctx); err != nil {
		return files.AccessGrant{}, files.ErrUnavailable
	}
	return result, nil
}

// OpenAccess rechecks the share, file, owner, optional recipient, and exact browser
// session. Read-only sharing never creates ownership or alters logical quota.
func (s *Store) OpenAccess(ctx context.Context, token string, storage files.ContentStore) (files.OpenContent, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	state, binding, err := s.admit(ctx)
	if err != nil {
		return files.OpenContent{}, err
	}
	digest, err := tokenDigest(token)
	if err != nil {
		return files.OpenContent{}, err
	}
	tx, c, err := s.lockCapability(ctx, digest, binding, state, true)
	if err != nil {
		return files.OpenContent{}, err
	}
	defer rollback(tx)
	var mode string
	err = tx.QueryRow(ctx, "SELECT mode FROM vault.shared_access WHERE token_hash=$1 AND share_id=$2 AND session_hash=$3 AND expires_at>clock_timestamp() FOR UPDATE", digest, c.share.ID, binding).Scan(&mode)
	if errors.Is(err, pgx.ErrNoRows) {
		return files.OpenContent{}, files.ErrNotFound
	}
	if err != nil {
		return files.OpenContent{}, files.ErrUnavailable
	}
	if mode == "PREVIEW" && (c.share.Permission != "PREVIEW_AND_DOWNLOAD" || !files.PreviewAllowed(c.file.DetectedMIME)) {
		return files.OpenContent{}, files.ErrPreviewUnsupported
	}
	handle, err := storage.Open(ctx, c.key, c.file.SizeBytes)
	if err != nil {
		return files.OpenContent{}, files.ErrUnavailable
	}
	success := false
	defer func() {
		if !success {
			_ = handle.Close()
		}
	}()
	if mode == "DOWNLOAD" && files.IsDownloadGET(ctx) {
		counted, err := tx.Exec(ctx, "UPDATE vault.shared_access SET download_counted=true WHERE token_hash=$1 AND NOT download_counted", digest)
		if err != nil {
			return files.OpenContent{}, files.ErrUnavailable
		}
		if counted.RowsAffected() == 1 {
			if _, err = tx.Exec(ctx, `INSERT INTO vault.file_download_counts(file_id,shared_downloads) VALUES($1,1)
 ON CONFLICT(file_id) DO UPDATE SET shared_downloads=vault.file_download_counts.shared_downloads+1`, c.file.ID); err != nil {
				return files.OpenContent{}, files.ErrUnavailable
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return files.OpenContent{}, files.ErrUnavailable
	}
	success = true
	return files.OpenContent{File: c.file, Mode: mode, Body: handle}, nil
}
