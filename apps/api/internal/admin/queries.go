package admin

import (
	"context"
	"strconv"
	"time"

	"full-stack-file-vault.local/api/internal/files"
)

func (s *Store) Users(ctx context.Context, first int, after *string) (UserPage, error) {
	if err := pageInput(first, after); err != nil {
		return UserPage{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, _, err := s.begin(ctx, nil)
	if err != nil {
		return UserPage{}, err
	}
	defer rollback(tx)
	rows, err := tx.Query(ctx, "SELECT "+userColumns+" FROM vault.users u LEFT JOIN vault.credentials c ON c.user_id=u.id WHERE ($1::uuid IS NULL OR u.id>$1) ORDER BY u.id LIMIT $2", after, first+1)
	if err != nil {
		return UserPage{}, files.ErrUnavailable
	}
	result := UserPage{Nodes: make([]User, 0, first)}
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			rows.Close()
			return UserPage{}, files.ErrUnavailable
		}
		result.Nodes = append(result.Nodes, user)
	}
	rows.Close()
	if rows.Err() != nil {
		return UserPage{}, files.ErrUnavailable
	}
	if len(result.Nodes) > first {
		result.HasNextPage = true
		result.Nodes = result.Nodes[:first]
	}
	if len(result.Nodes) > 0 {
		last := result.Nodes[len(result.Nodes)-1].ID
		result.EndCursor = &last
	}
	if err = tx.Commit(ctx); err != nil {
		return UserPage{}, files.ErrUnavailable
	}
	return result, nil
}
func (s *Store) Files(ctx context.Context, first int, after, ownerID *string) (FilePage, error) {
	if err := pageInput(first, after); err != nil {
		return FilePage{}, err
	}
	if ownerID != nil && !uuidPattern.MatchString(*ownerID) {
		return FilePage{}, files.ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, _, err := s.begin(ctx, nil)
	if err != nil {
		return FilePage{}, err
	}
	defer rollback(tx)
	rows, err := tx.Query(ctx, `SELECT f.id::text,f.original_name,b.size_bytes,COALESCE(b.detected_mime,'application/octet-stream'),
 f.created_at,f.owner_id::text,c.login_name,COALESCE(d.shared_downloads,0)::text
 FROM vault.files f JOIN vault.blobs b ON b.id=f.blob_id LEFT JOIN vault.credentials c ON c.user_id=f.owner_id
 LEFT JOIN vault.file_download_counts d ON d.file_id=f.id
 WHERE ($1::uuid IS NULL OR f.id>$1) AND ($2::uuid IS NULL OR f.owner_id=$2) ORDER BY f.id LIMIT $3`, after, ownerID, first+1)
	if err != nil {
		return FilePage{}, files.ErrUnavailable
	}
	result := FilePage{Nodes: make([]File, 0, first)}
	for rows.Next() {
		var file File
		if err = rows.Scan(&file.File.ID, &file.File.Name, &file.File.SizeBytes, &file.File.DetectedMIME, &file.File.CreatedAt, &file.OwnerID, &file.LoginName, &file.DownloadStarts); err != nil {
			rows.Close()
			return FilePage{}, files.ErrUnavailable
		}
		result.Nodes = append(result.Nodes, file)
	}
	rows.Close()
	if rows.Err() != nil {
		return FilePage{}, files.ErrUnavailable
	}
	if len(result.Nodes) > first {
		result.HasNextPage = true
		result.Nodes = result.Nodes[:first]
	}
	if len(result.Nodes) > 0 {
		last := result.Nodes[len(result.Nodes)-1].File.ID
		result.EndCursor = &last
	}
	if err = tx.Commit(ctx); err != nil {
		return FilePage{}, files.ErrUnavailable
	}
	return result, nil
}
func (s *Store) Statistics(ctx context.Context) (Statistics, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, _, err := s.begin(ctx, nil)
	if err != nil {
		return Statistics{}, err
	}
	defer rollback(tx)
	var result Statistics
	err = tx.QueryRow(ctx, `WITH logical AS (SELECT COALESCE(sum(b.size_bytes),0) AS bytes,count(*) AS files FROM vault.files f JOIN vault.blobs b ON b.id=f.blob_id),
 referenced AS (SELECT COALESCE(sum(size_bytes),0) AS bytes FROM vault.blobs b WHERE EXISTS(SELECT 1 FROM vault.files f WHERE f.blob_id=b.id))
 SELECT (SELECT count(*)::text FROM vault.users),logical.files::text,logical.bytes::text,referenced.bytes::text,
 (SELECT COALESCE(sum(size_bytes),0)::text FROM vault.blobs WHERE state='GC_PENDING'),
 (logical.bytes-referenced.bytes)::text,
 CASE WHEN logical.bytes=0 THEN '0.00' ELSE round(100*(logical.bytes-referenced.bytes)/logical.bytes,2)::text END,
 (SELECT COALESCE(sum(shared_downloads),0)::text FROM vault.file_download_counts) FROM logical,referenced`).Scan(
		&result.UserCount, &result.FileCount, &result.LogicalBytes, &result.ReferencedBytes, &result.PendingDeletionBytes, &result.SavedBytes, &result.SavingsPercent, &result.DownloadStarts)
	if err != nil {
		return Statistics{}, files.ErrUnavailable
	}
	if err = tx.Commit(ctx); err != nil {
		return Statistics{}, files.ErrUnavailable
	}
	return result, nil
}
func (s *Store) Audit(ctx context.Context, first int, after *string) (AuditPage, error) {
	if first < 1 || first > 50 {
		return AuditPage{}, files.ErrInvalidInput
	}
	var before *int64
	if after != nil {
		value, err := strconv.ParseInt(*after, 10, 64)
		if err != nil || value < 1 || strconv.FormatInt(value, 10) != *after {
			return AuditPage{}, files.ErrInvalidInput
		}
		before = &value
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, _, err := s.begin(ctx, nil)
	if err != nil {
		return AuditPage{}, err
	}
	defer rollback(tx)
	rows, err := tx.Query(ctx, `SELECT id::text,actor_id::text,target_user_id::text,action,occurred_at,
 previous_quota::text,new_quota::text,previous_disabled_at,new_disabled_at,revoked_sessions::text,revoked_shares::text
 FROM vault.admin_audit WHERE ($1::bigint IS NULL OR id<$1) ORDER BY id DESC LIMIT $2`, before, first+1)
	if err != nil {
		return AuditPage{}, files.ErrUnavailable
	}
	result := AuditPage{Nodes: make([]Audit, 0, first)}
	for rows.Next() {
		var entry Audit
		if err = rows.Scan(&entry.ID, &entry.ActorID, &entry.TargetUserID, &entry.Action, &entry.OccurredAt, &entry.PreviousQuota, &entry.NewQuota, &entry.PreviousDisabledAt, &entry.NewDisabledAt, &entry.RevokedSessions, &entry.RevokedShares); err != nil {
			rows.Close()
			return AuditPage{}, files.ErrUnavailable
		}
		result.Nodes = append(result.Nodes, entry)
	}
	rows.Close()
	if rows.Err() != nil {
		return AuditPage{}, files.ErrUnavailable
	}
	if len(result.Nodes) > first {
		result.HasNextPage = true
		result.Nodes = result.Nodes[:first]
	}
	if len(result.Nodes) > 0 {
		last := result.Nodes[len(result.Nodes)-1].ID
		result.EndCursor = &last
	}
	if err = tx.Commit(ctx); err != nil {
		return AuditPage{}, files.ErrUnavailable
	}
	return result, nil
}
