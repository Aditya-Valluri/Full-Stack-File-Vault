package sharing

import (
	"context"
	"errors"
	"time"

	"full-stack-file-vault.local/api/internal/files"
	"github.com/jackc/pgx/v5"
)

// Create issues a read-only link once. Recipient nil means anyone holding the
// link; an account restriction still requires possession of the unpredictable token.
func (s *Store) Create(ctx context.Context, fileID, permission string, expiresInSeconds int, recipient *string) (Created, error) {
	if !validID(fileID) || expiresInSeconds < 60 || expiresInSeconds > 2592000 ||
		(permission != "DOWNLOAD" && permission != "PREVIEW_AND_DOWNLOAD" && permission != "PREVIEW_ONLY") || (recipient != nil && !validID(*recipient)) {
		return Created{}, files.ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, user, err := s.ownerTransaction(ctx)
	if err != nil {
		return Created{}, err
	}
	defer rollback(tx)
	var exists bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM vault.files WHERE id=$1 AND owner_id=$2)", fileID, user.UserID).Scan(&exists); err != nil {
		return Created{}, files.ErrUnavailable
	}
	if !exists {
		return Created{}, files.ErrNotFound
	}
	if permission == "PREVIEW_ONLY" {
		var name, media string
		if err = tx.QueryRow(ctx, "SELECT f.original_name,COALESCE(b.detected_mime,'application/octet-stream') FROM vault.files f JOIN vault.blobs b ON b.id=f.blob_id WHERE f.id=$1 AND f.owner_id=$2", fileID, user.UserID).Scan(&name, &media); err != nil {
			return Created{}, files.ErrUnavailable
		}
		if !files.FilePreviewAllowed(name, media) {
			return Created{}, files.ErrPreviewUnsupported
		}
	}
	if recipient != nil {
		if *recipient == user.UserID {
			return Created{}, files.ErrInvalidInput
		}
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM vault.users WHERE id=$1 AND disabled_at IS NULL)", *recipient).Scan(&exists); err != nil {
			return Created{}, files.ErrUnavailable
		}
		if !exists {
			return Created{}, files.ErrInvalidInput
		}
	}
	// The owner lock serializes creation and keeps the retained set bounded at 20/file.
	if _, err = tx.Exec(ctx, "DELETE FROM vault.file_shares WHERE file_id=$1 AND expires_at<=clock_timestamp()", fileID); err != nil {
		return Created{}, files.ErrUnavailable
	}
	var count int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM vault.file_shares WHERE file_id=$1", fileID).Scan(&count); err != nil {
		return Created{}, files.ErrUnavailable
	}
	if count >= 20 {
		return Created{}, ErrLimit
	}
	token, digest, err := newToken()
	if err != nil {
		return Created{}, err
	}
	result := Created{URL: "/share#" + token}
	err = tx.QueryRow(ctx, `WITH instant AS MATERIALIZED(SELECT clock_timestamp() AS t)
 INSERT INTO vault.file_shares(file_id,owner_id,token_hash,recipient_id,permission,created_at,expires_at)
 SELECT $1,$2,$3,$4,$5,t,t+make_interval(secs=>$6) FROM instant
 RETURNING id::text,recipient_id::text,permission,created_at,expires_at`, fileID, user.UserID, digest, recipient, permission, expiresInSeconds).
		Scan(&result.Share.ID, &result.Share.RecipientID, &result.Share.Permission, &result.Share.CreatedAt, &result.Share.ExpiresAt)
	if err != nil {
		return Created{}, files.ErrUnavailable
	}
	if err = tx.Commit(ctx); err != nil {
		return Created{}, files.ErrUnavailable
	}
	return result, nil
}
func (s *Store) List(ctx context.Context, fileID string) (Overview, error) {
	if !validID(fileID) {
		return Overview{}, files.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, user, err := s.ownerTransaction(ctx)
	if err != nil {
		return Overview{}, err
	}
	defer rollback(tx)
	result := Overview{Shares: make([]Share, 0)}
	err = tx.QueryRow(ctx, `SELECT COALESCE(d.shared_downloads,0) FROM vault.files f
 LEFT JOIN vault.file_download_counts d ON d.file_id=f.id WHERE f.id=$1 AND f.owner_id=$2`, fileID, user.UserID).Scan(&result.DownloadStarts)
	if errors.Is(err, pgx.ErrNoRows) {
		return Overview{}, files.ErrNotFound
	}
	if err != nil {
		return Overview{}, files.ErrUnavailable
	}
	rows, err := tx.Query(ctx, "SELECT id::text,recipient_id::text,permission,created_at,expires_at FROM vault.file_shares WHERE file_id=$1 AND owner_id=$2 AND expires_at>clock_timestamp() ORDER BY created_at DESC,id DESC LIMIT 20", fileID, user.UserID)
	if err != nil {
		return Overview{}, files.ErrUnavailable
	}
	for rows.Next() {
		var sh Share
		if err = rows.Scan(&sh.ID, &sh.RecipientID, &sh.Permission, &sh.CreatedAt, &sh.ExpiresAt); err != nil {
			rows.Close()
			return Overview{}, files.ErrUnavailable
		}
		result.Shares = append(result.Shares, sh)
	}
	rows.Close()
	if rows.Err() != nil {
		return Overview{}, files.ErrUnavailable
	}
	result.Activity, err = readActivity(ctx, tx, fileID)
	if err != nil {
		return Overview{}, files.ErrUnavailable
	}
	if err = tx.Commit(ctx); err != nil {
		return Overview{}, files.ErrUnavailable
	}
	return result, nil
}
func (s *Store) Revoke(ctx context.Context, id string) error {
	if !validID(id) {
		return files.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, user, err := s.ownerTransaction(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	// Cascading removal revokes every previously issued shared byte grant atomically.
	result, err := tx.Exec(ctx, "DELETE FROM vault.file_shares WHERE id=$1 AND owner_id=$2", id, user.UserID)
	if err != nil {
		return files.ErrUnavailable
	}
	if result.RowsAffected() != 1 {
		return files.ErrNotFound
	}
	if err = tx.Commit(ctx); err != nil {
		return files.ErrUnavailable
	}
	return nil
}
