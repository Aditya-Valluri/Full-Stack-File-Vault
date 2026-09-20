// Package files reads owner-scoped logical metadata without exposing blob identity.
package files

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"file-vault.local/api/internal/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalidInput = errors.New("invalid file query")
	ErrNotFound     = errors.New("file not found")
	ErrUnavailable  = errors.New("file metadata unavailable")
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, ErrUnavailable
	}
	return &Store{pool: pool}, nil
}

type File struct {
	ID, Name, DetectedMIME string
	Tags                   []string
	SizeBytes              int64
	CreatedAt              time.Time
}
type Page struct {
	Nodes       []File
	EndCursor   *string
	HasNextPage bool
}
type Filter struct {
	NameContains               *string
	UploaderNameContains       *string
	TagsAll                    []string
	MIMEType                   *string
	MinSizeBytes, MaxSizeBytes *string
	CreatedFrom, CreatedBefore *time.Time
}
type ListOptions struct {
	First  int
	After  *string
	Filter Filter
}

// List uses a unique newest-first keyset and reads only first+1 rows. Cursors are
// positions, never authorization. Every SQL statement includes the trusted owner.
func (s *Store) List(ctx context.Context, options ListOptions) (Page, error) {
	identity, err := auth.RequireUser(ctx)
	if err != nil {
		return Page{}, err
	}
	if options.First < 1 || options.First > 50 {
		return Page{}, ErrInvalidInput
	}
	filter, err := validateFilter(options.Filter)
	if err != nil {
		return Page{}, err
	}
	var after *cursor
	if options.After != nil {
		value, err := decodeCursor(*options.After, identity.UserID)
		if err != nil {
			return Page{}, err
		}
		after = &value
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, identity, err := s.begin(ctx)
	if err != nil {
		return Page{}, err
	}
	defer rollback(tx)
	args := []any{identity.UserID}
	query := selectMetadata + " WHERE f.owner_id=$1"
	add := func(clause string, value any) {
		args = append(args, value)
		query += " AND " + fmt.Sprintf(clause, len(args))
	}
	if after != nil {
		args = append(args, after.CreatedAt, after.ID)
		query += fmt.Sprintf(" AND (f.created_at,f.id)<($%d::timestamptz,$%d::uuid)", len(args)-1, len(args))
	}
	if filter.name != "" {
		add("f.original_name ILIKE $%d ESCAPE '!'", "%"+escapeLike(filter.name)+"%")
	}
	if filter.uploader != "" {
		add("EXISTS(SELECT 1 FROM vault.credentials c WHERE c.user_id=f.owner_id AND c.login_name ILIKE $%d ESCAPE '!')", "%"+escapeLike(filter.uploader)+"%")
	}
	for _, tag := range filter.tags {
		add("EXISTS(SELECT 1 FROM vault.file_tags t WHERE t.file_id=f.id AND t.tag=$%d)", tag)
	}
	if filter.mime != "" {
		add("split_part(COALESCE(b.detected_mime,'application/octet-stream'),';',1)=$%d", filter.mime)
	}
	if filter.min != nil {
		add("b.size_bytes >= $%d", *filter.min)
	}
	if filter.max != nil {
		add("b.size_bytes <= $%d", *filter.max)
	}
	if options.Filter.CreatedFrom != nil {
		add("f.created_at >= $%d", *options.Filter.CreatedFrom)
	}
	if options.Filter.CreatedBefore != nil {
		add("f.created_at < $%d", *options.Filter.CreatedBefore)
	}
	args = append(args, options.First+1)
	query += fmt.Sprintf(" ORDER BY f.created_at DESC,f.id DESC LIMIT $%d", len(args))
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return Page{}, ErrUnavailable
	}
	page := Page{Nodes: make([]File, 0, options.First)}
	for rows.Next() {
		var file File
		if err = rows.Scan(&file.ID, &file.Name, &file.SizeBytes, &file.DetectedMIME, &file.CreatedAt, &file.Tags); err != nil {
			rows.Close()
			return Page{}, ErrUnavailable
		}
		page.Nodes = append(page.Nodes, file)
	}
	rows.Close()
	if rows.Err() != nil {
		return Page{}, ErrUnavailable
	}
	if len(page.Nodes) > options.First {
		page.HasNextPage = true
		page.Nodes = page.Nodes[:options.First]
	}
	if len(page.Nodes) > 0 {
		last := page.Nodes[len(page.Nodes)-1]
		encoded, err := encodeCursor(cursor{Version: 1, Owner: identity.UserID, CreatedAt: last.CreatedAt, ID: last.ID})
		if err != nil {
			return Page{}, ErrUnavailable
		}
		page.EndCursor = &encoded
	}
	if err = tx.Commit(ctx); err != nil {
		return Page{}, ErrUnavailable
	}
	return page, nil
}

// Get deliberately gives foreign and nonexistent IDs the same result, even for
// admins. Admin-wide visibility requires a separate explicitly authorized API.
func (s *Store) Get(ctx context.Context, id string) (File, error) {
	if _, err := auth.RequireUser(ctx); err != nil {
		return File{}, err
	}
	if !validID(id) {
		return File{}, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, identity, err := s.begin(ctx)
	if err != nil {
		return File{}, err
	}
	defer rollback(tx)
	var file File
	err = tx.QueryRow(ctx, selectMetadata+" WHERE f.owner_id=$1 AND f.id=$2", identity.UserID, id).Scan(&file.ID, &file.Name, &file.SizeBytes, &file.DetectedMIME, &file.CreatedAt, &file.Tags)
	if errors.Is(err, pgx.ErrNoRows) {
		return File{}, ErrNotFound
	}
	if err != nil {
		return File{}, ErrUnavailable
	}
	if err = tx.Commit(ctx); err != nil {
		return File{}, ErrUnavailable
	}
	return file, nil
}

const selectMetadata = "SELECT f.id::text,f.original_name,b.size_bytes,COALESCE(b.detected_mime,'application/octet-stream'),f.created_at,ARRAY(SELECT t.tag FROM vault.file_tags t WHERE t.file_id=f.id ORDER BY t.tag) FROM vault.files f JOIN vault.blobs b ON b.id=f.blob_id"

// Reuse the existing user/session lock protocol for fresh authorization. This
// serializes reads with publication for one user; keep statements and pages bounded.
func (s *Store) begin(ctx context.Context) (pgx.Tx, auth.Session, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, auth.Session{}, ErrUnavailable
	}
	if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout='2s'; SET LOCAL statement_timeout='3s'"); err != nil {
		rollback(tx)
		return nil, auth.Session{}, ErrUnavailable
	}
	identity, err := auth.LockPublicationIdentity(ctx, tx)
	if err != nil {
		rollback(tx)
		return nil, auth.Session{}, err
	}
	return tx, identity, nil
}
func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
func escapeLike(value string) string {
	return strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(value)
}
