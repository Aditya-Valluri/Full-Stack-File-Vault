// Package sharing grants read-only access to explicitly shared logical files.
package sharing

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"regexp"
	"time"

	"full-stack-file-vault.local/api/internal/auth"
	"full-stack-file-vault.local/api/internal/files"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrLimit = errors.New("sharing capacity reached")

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, files.ErrUnavailable
	}
	return &Store{pool: pool}, nil
}

type Share struct {
	ID                   string
	RecipientID          *string
	Permission           string
	CreatedAt, ExpiresAt time.Time
}
type Created struct {
	Share Share
	URL   string
}
type Overview struct {
	Activity       []Activity
	Shares         []Share
	DownloadStarts int64
}
type SharedFile struct {
	Name, DetectedMIME string
	SizeBytes          int64
	PreviewAllowed     bool
	ExpiresAt          time.Time
}
type capability struct {
	share           Share
	file            files.File
	owner, key      string
	sessionDeadline time.Time
}

var uuidPattern = regexp.MustCompile("^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")

func validID(id string) bool { return uuidPattern.MatchString(id) }
func tokenDigest(token string) ([]byte, error) {
	if len(token) != 43 {
		return nil, files.ErrNotFound
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(raw) != 32 {
		return nil, files.ErrNotFound
	}
	digest := sha256.Sum256(raw)
	return digest[:], nil
}
func newToken() (string, []byte, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", nil, files.ErrUnavailable
	}
	digest := sha256.Sum256(raw[:])
	return base64.RawURLEncoding.EncodeToString(raw[:]), digest[:], nil
}
func (s *Store) transaction(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, files.ErrUnavailable
	}
	if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout='2s'; SET LOCAL statement_timeout='3s'"); err != nil {
		rollback(tx)
		return nil, files.ErrUnavailable
	}
	return tx, nil
}
func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
func (s *Store) ownerTransaction(ctx context.Context) (pgx.Tx, auth.Session, error) {
	tx, err := s.transaction(ctx)
	if err != nil {
		return nil, auth.Session{}, err
	}
	user, err := auth.LockPublicationIdentity(ctx, tx)
	if err != nil {
		rollback(tx)
		return nil, auth.Session{}, err
	}
	return tx, user, nil
}

// admit charges anonymous sharing attempts before capability lookup in an independent
// transaction: an invalid token must not roll back its abuse budget. Authenticated
// requests already use the existing global per-user HTTP admission middleware.
func (s *Store) admit(ctx context.Context) (auth.Session, []byte, error) {
	state, binding, err := auth.BrowserBinding(ctx)
	if err != nil {
		return state, nil, err
	}
	if state.UserID != "" {
		return state, binding, nil
	}
	tx, err := s.transaction(ctx)
	if err != nil {
		return state, nil, err
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, "INSERT INTO vault.shared_request_windows(session_hash) VALUES($1) ON CONFLICT DO NOTHING", binding); err != nil {
		return state, nil, files.ErrUnavailable
	}
	var accepted []time.Time
	if err = tx.QueryRow(ctx, "SELECT accepted_at FROM vault.shared_request_windows WHERE session_hash=$1 FOR UPDATE", binding).Scan(&accepted); err != nil {
		return state, nil, files.ErrUnavailable
	}
	var now time.Time
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return state, nil, files.ErrUnavailable
	}
	if len(accepted) > 0 && now.Before(accepted[len(accepted)-1]) {
		now = accepted[len(accepted)-1]
	}
	fresh := make([]time.Time, 0, 2)
	for _, at := range accepted {
		if at.After(now.Add(-time.Second)) {
			fresh = append(fresh, at)
		}
	}
	if len(fresh) >= 2 {
		return state, nil, &auth.RateLimitError{RetryAfter: fresh[0].Add(time.Second).Sub(now)}
	}
	fresh = append(fresh, now)
	if _, err = tx.Exec(ctx, "UPDATE vault.shared_request_windows SET accepted_at=$2 WHERE session_hash=$1", binding, fresh); err != nil {
		return state, nil, files.ErrUnavailable
	}
	if err = tx.Commit(ctx); err != nil {
		return state, nil, files.ErrUnavailable
	}
	return state, binding, nil
}

// lockCapability acquires all participating user rows in UUID order, then the
// browser session, then the share. This extends the existing lock order without
// a recipient->owner inversion when two users share with each other.
func (s *Store) lockCapability(ctx context.Context, digest, binding []byte, state auth.Session, access bool) (pgx.Tx, capability, error) {
	var c capability
	tx, err := s.transaction(ctx)
	if err != nil {
		return nil, c, err
	}
	ok := false
	defer func() {
		if !ok {
			rollback(tx)
		}
	}()
	query := "SELECT id::text,owner_id::text FROM vault.file_shares WHERE token_hash=$1"
	args := []any{digest}
	if access {
		query = "SELECT sh.id::text,sh.owner_id::text FROM vault.shared_access a JOIN vault.file_shares sh ON sh.id=a.share_id WHERE a.token_hash=$1 AND a.session_hash=$2"
		args = append(args, binding)
	}
	if err = tx.QueryRow(ctx, query, args...).Scan(&c.share.ID, &c.owner); errors.Is(err, pgx.ErrNoRows) {
		return nil, c, files.ErrNotFound
	} else if err != nil {
		return nil, c, files.ErrUnavailable
	}
	users := []string{c.owner}
	if state.UserID != "" && state.UserID != c.owner {
		users = append(users, state.UserID)
	}
	rows, err := tx.Query(ctx, "SELECT id::text FROM vault.users WHERE id=ANY($1::uuid[]) ORDER BY id FOR UPDATE", users)
	if err != nil {
		return nil, c, files.ErrUnavailable
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, c, files.ErrUnavailable
		}
	}
	rows.Close()
	if rows.Err() != nil {
		return nil, c, files.ErrUnavailable
	}
	if state.UserID != "" {
		if _, err = auth.LockPublicationIdentity(ctx, tx); err != nil {
			return nil, c, err
		}
	} else {
		var locked []byte
		if err = tx.QueryRow(ctx, "SELECT token_hash FROM vault.sessions WHERE token_hash=$1 FOR UPDATE", binding).Scan(&locked); err != nil {
			return nil, c, auth.ErrUnauthenticated
		}
	}
	// Fresh SQL after locks rechecks anonymous expiry too; snapshots alone are insufficient.
	var sessionUser *string
	err = tx.QueryRow(ctx, "SELECT user_id::text,LEAST(expires_at,idle_expires_at) FROM vault.sessions WHERE token_hash=$1 AND revoked_at IS NULL AND expires_at>clock_timestamp() AND idle_expires_at>clock_timestamp()", binding).Scan(&sessionUser, &c.sessionDeadline)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, c, auth.ErrUnauthenticated
	}
	if err != nil {
		return nil, c, files.ErrUnavailable
	}
	if (sessionUser == nil && state.UserID != "") || (sessionUser != nil && *sessionUser != state.UserID) {
		return nil, c, auth.ErrUnauthenticated
	}
	err = tx.QueryRow(ctx, `SELECT sh.recipient_id::text,sh.permission,sh.created_at,sh.expires_at,
 f.id::text,f.original_name,b.size_bytes,COALESCE(b.detected_mime,'application/octet-stream'),f.created_at,b.storage_key
 FROM vault.file_shares sh JOIN vault.files f ON f.id=sh.file_id AND f.owner_id=sh.owner_id
 JOIN vault.users u ON u.id=sh.owner_id JOIN vault.blobs b ON b.id=f.blob_id
 WHERE sh.id=$1 AND sh.owner_id=$2 AND sh.expires_at>clock_timestamp()
 AND u.disabled_at IS NULL AND b.state='ACTIVE' FOR UPDATE OF sh`, c.share.ID, c.owner).Scan(
		&c.share.RecipientID, &c.share.Permission, &c.share.CreatedAt, &c.share.ExpiresAt,
		&c.file.ID, &c.file.Name, &c.file.SizeBytes, &c.file.DetectedMIME, &c.file.CreatedAt, &c.key)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, c, files.ErrNotFound
	}
	if err != nil {
		return nil, c, files.ErrUnavailable
	}
	if c.share.RecipientID != nil && *c.share.RecipientID != state.UserID {
		return nil, c, files.ErrNotFound
	}
	ok = true
	return tx, c, nil
}
