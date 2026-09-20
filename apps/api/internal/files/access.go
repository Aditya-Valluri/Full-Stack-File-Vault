package files

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"time"

	"file-vault.local/api/internal/auth"
	"github.com/jackc/pgx/v5"
)

var (
	ErrPreviewUnsupported = errors.New("preview is unavailable for this content type")
	ErrAccessLimit        = errors.New("too many active file access grants")
)

type AccessGrant struct {
	URL       string
	ExpiresAt time.Time
}
type ContentStore interface {
	Open(context.Context, string, int64) (io.ReadSeekCloser, error)
}
type OpenContent struct {
	File File
	Mode string
	Body io.ReadSeekCloser
}

func PreviewAllowed(media string) bool {
	switch strings.SplitN(media, ";", 2)[0] {
	case "image/png", "image/jpeg", "image/webp":
		return true
	default:
		return false
	}
}

func (s *Store) CreateAccess(ctx context.Context, id, mode string) (AccessGrant, error) {
	if _, err := auth.RequireUser(ctx); err != nil {
		return AccessGrant{}, err
	}
	if !validID(id) {
		return AccessGrant{}, ErrNotFound
	}
	if mode != "DOWNLOAD" && mode != "PREVIEW" {
		return AccessGrant{}, ErrInvalidInput
	}
	binding, err := auth.SessionBinding(ctx)
	if err != nil {
		return AccessGrant{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, identity, err := s.begin(ctx)
	if err != nil {
		return AccessGrant{}, err
	}
	defer rollback(tx)
	var detected string
	err = tx.QueryRow(ctx, "SELECT COALESCE(b.detected_mime,'application/octet-stream') FROM vault.files f JOIN vault.blobs b ON b.id=f.blob_id WHERE f.id=$1 AND f.owner_id=$2 AND b.state='ACTIVE'", id, identity.UserID).Scan(&detected)
	if errors.Is(err, pgx.ErrNoRows) {
		return AccessGrant{}, ErrNotFound
	}
	if err != nil {
		return AccessGrant{}, ErrUnavailable
	}
	if mode == "PREVIEW" && !PreviewAllowed(detected) {
		return AccessGrant{}, ErrPreviewUnsupported
	}
	if _, err = tx.Exec(ctx, "DELETE FROM vault.file_access WHERE owner_id=$1 AND expires_at<=clock_timestamp()", identity.UserID); err != nil {
		return AccessGrant{}, ErrUnavailable
	}
	var active int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM vault.file_access WHERE owner_id=$1", identity.UserID).Scan(&active); err != nil {
		return AccessGrant{}, ErrUnavailable
	}
	if active >= 64 {
		return AccessGrant{}, ErrAccessLimit
	}
	var random [32]byte
	if _, err = rand.Read(random[:]); err != nil {
		return AccessGrant{}, ErrUnavailable
	}
	digest := sha256.Sum256(random[:])
	token := base64.RawURLEncoding.EncodeToString(random[:])
	result := AccessGrant{URL: "/content/" + token}
	err = tx.QueryRow(ctx, `WITH instant AS MATERIALIZED (SELECT clock_timestamp() AS t)
 INSERT INTO vault.file_access(token_hash,owner_id,session_hash,file_id,mode,created_at,expires_at)
 SELECT $1,$2,$3,$4,$5,t,LEAST(t+interval '1 minute',s.expires_at,s.idle_expires_at)
 FROM vault.sessions s CROSS JOIN instant WHERE s.token_hash=$3 AND s.user_id=$2
 AND s.revoked_at IS NULL AND s.expires_at>t AND s.idle_expires_at>t
 RETURNING expires_at`, digest[:], identity.UserID, binding, id, mode).Scan(&result.ExpiresAt)
	if err != nil {
		return AccessGrant{}, ErrUnavailable
	}
	if err = tx.Commit(ctx); err != nil {
		return AccessGrant{}, ErrUnavailable
	}
	return result, nil
}

// OpenAccess validates the grant/session/file at request admission and opens bytes
// while the owner lock prevents logical deletion. Release the DB transaction before
// streaming: an already-open transfer may finish after deletion or session revocation.
func (s *Store) OpenAccess(ctx context.Context, token string, storage ContentStore) (OpenContent, error) {
	if _, err := auth.RequireUser(ctx); err != nil {
		return OpenContent{}, err
	}
	if len(token) != 43 {
		return OpenContent{}, ErrNotFound
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(raw) != 32 {
		return OpenContent{}, ErrNotFound
	}
	digest := sha256.Sum256(raw)
	binding, err := auth.SessionBinding(ctx)
	if err != nil {
		return OpenContent{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, identity, err := s.begin(ctx)
	if err != nil {
		return OpenContent{}, err
	}
	defer rollback(tx)
	var result OpenContent
	var key string
	err = tx.QueryRow(ctx, `SELECT f.id::text,f.original_name,b.size_bytes,COALESCE(b.detected_mime,'application/octet-stream'),f.created_at,g.mode,b.storage_key
 FROM vault.file_access g JOIN vault.files f ON f.id=g.file_id JOIN vault.blobs b ON b.id=f.blob_id
 WHERE g.token_hash=$1 AND g.owner_id=$2 AND f.owner_id=$2 AND g.session_hash=$3
 AND g.expires_at>clock_timestamp() AND b.state='ACTIVE'`, digest[:], identity.UserID, binding).Scan(&result.File.ID, &result.File.Name, &result.File.SizeBytes, &result.File.DetectedMIME, &result.File.CreatedAt, &result.Mode, &key)
	if errors.Is(err, pgx.ErrNoRows) {
		return OpenContent{}, ErrNotFound
	}
	if err != nil {
		return OpenContent{}, ErrUnavailable
	}
	if result.Mode == "PREVIEW" && !PreviewAllowed(result.File.DetectedMIME) {
		return OpenContent{}, ErrPreviewUnsupported
	}
	result.Body, err = storage.Open(ctx, key, result.File.SizeBytes)
	if err != nil {
		return OpenContent{}, ErrUnavailable
	}
	if err = tx.Commit(ctx); err != nil {
		_ = result.Body.Close()
		return OpenContent{}, ErrUnavailable
	}
	return result, nil
}
