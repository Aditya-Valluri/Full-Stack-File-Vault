package files

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Folder struct {
	ID       string
	ParentID *string
	Name     string
}

// Folder names are display metadata, never filesystem paths.
func folderName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if !utf8.ValidString(name) || len(name) > 400 || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 100 || name == "." || name == ".." {
		return "", ErrInvalidInput
	}
	for _, r := range name {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			return "", ErrInvalidInput
		}
	}
	return name, nil
}
func folderError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && (pg.Code == "23505" || pg.Code == "23503") {
		return ErrInvalidInput
	}
	return ErrUnavailable
}

// A hard per-owner cap makes the complete organization tree bounded. Querying
// it once avoids recursive/N+1 folder lookups in the browser.
func (s *Store) Folders(ctx context.Context) ([]Folder, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, user, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	rows, err := tx.Query(ctx, "SELECT id::text,parent_id::text,name FROM vault.folders WHERE owner_id=$1 ORDER BY name,id LIMIT 1000", user.UserID)
	if err != nil {
		return nil, ErrUnavailable
	}
	result := make([]Folder, 0)
	for rows.Next() {
		var f Folder
		if err = rows.Scan(&f.ID, &f.ParentID, &f.Name); err != nil {
			rows.Close()
			return nil, ErrUnavailable
		}
		result = append(result, f)
	}
	rows.Close()
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, ErrUnavailable
	}
	return result, nil
}

func (s *Store) CreateFolder(ctx context.Context, raw string, parent *string) (Folder, error) {
	name, err := folderName(raw)
	if err != nil {
		return Folder{}, err
	}
	if parent != nil && !validID(*parent) {
		return Folder{}, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, user, err := s.begin(ctx)
	if err != nil {
		return Folder{}, err
	}
	defer rollback(tx)
	var count int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM vault.folders WHERE owner_id=$1", user.UserID).Scan(&count); err != nil {
		return Folder{}, ErrUnavailable
	}
	if count >= 1000 {
		return Folder{}, ErrInvalidInput
	}
	if parent != nil {
		// Parents cannot be moved. The existing tree is therefore acyclic; bound depth
		// anyway so a malformed operator-written tree cannot cause unbounded recursion.
		var depth int
		err = tx.QueryRow(ctx, `WITH RECURSIVE ancestors AS (
   SELECT id,parent_id,1 AS depth FROM vault.folders WHERE owner_id=$1 AND id=$2
   UNION ALL SELECT f.id,f.parent_id,a.depth+1 FROM vault.folders f
   JOIN ancestors a ON f.id=a.parent_id WHERE f.owner_id=$1 AND a.depth<20)
   SELECT COALESCE(max(depth),0) FROM ancestors`, user.UserID, *parent).Scan(&depth)
		if err != nil {
			return Folder{}, ErrUnavailable
		}
		if depth == 0 {
			return Folder{}, ErrNotFound
		}
		if depth >= 20 {
			return Folder{}, ErrInvalidInput
		}
	}
	var f Folder
	err = tx.QueryRow(ctx, "INSERT INTO vault.folders(owner_id,parent_id,name) VALUES($1,$2,$3) RETURNING id::text,parent_id::text,name", user.UserID, parent, name).Scan(&f.ID, &f.ParentID, &f.Name)
	if err != nil {
		return Folder{}, folderError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return Folder{}, ErrUnavailable
	}
	return f, nil
}
func (s *Store) RenameFolder(ctx context.Context, id, raw string) (Folder, error) {
	name, err := folderName(raw)
	if err != nil {
		return Folder{}, err
	}
	if !validID(id) {
		return Folder{}, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, user, err := s.begin(ctx)
	if err != nil {
		return Folder{}, err
	}
	defer rollback(tx)
	var f Folder
	err = tx.QueryRow(ctx, "UPDATE vault.folders SET name=$3 WHERE owner_id=$1 AND id=$2 RETURNING id::text,parent_id::text,name", user.UserID, id, name).Scan(&f.ID, &f.ParentID, &f.Name)
	if err != nil {
		return Folder{}, folderError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return Folder{}, ErrUnavailable
	}
	return f, nil
}
func (s *Store) DeleteFolder(ctx context.Context, id string) error {
	if !validID(id) {
		return ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, user, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	result, err := tx.Exec(ctx, "DELETE FROM vault.folders WHERE owner_id=$1 AND id=$2", user.UserID, id)
	// Foreign keys reject nonempty folders. Owner serialization also covers file moves.
	if err != nil {
		return folderError(err)
	}
	if result.RowsAffected() != 1 {
		return ErrNotFound
	}
	if err = tx.Commit(ctx); err != nil {
		return ErrUnavailable
	}
	return nil
}
func (s *Store) MoveFile(ctx context.Context, id string, folder *string) error {
	if !validID(id) || (folder != nil && !validID(*folder)) {
		return ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, user, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if folder != nil {
		var exists bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM vault.folders WHERE owner_id=$1 AND id=$2)", user.UserID, *folder).Scan(&exists); err != nil {
			return ErrUnavailable
		}
		if !exists {
			return ErrNotFound
		}
	}
	result, err := tx.Exec(ctx, "UPDATE vault.files SET folder_id=$3 WHERE owner_id=$1 AND id=$2", user.UserID, id, folder)
	if err != nil {
		return folderError(err)
	}
	if result.RowsAffected() != 1 {
		return ErrNotFound
	}
	if err = tx.Commit(ctx); err != nil {
		return ErrUnavailable
	}
	return nil
}
