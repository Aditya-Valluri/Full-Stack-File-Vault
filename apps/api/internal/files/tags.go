package files

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"

	"full-stack-file-vault.local/api/internal/auth"
	"github.com/jackc/pgx/v5"
)

var tagPattern = regexp.MustCompile("^[a-z0-9][a-z0-9 _-]{0,31}$")

// NormalizeTags bounds metadata and makes equality deterministic across clients.
// Empty input clears tags; duplicates collapse after trimming and ASCII folding.
func NormalizeTags(input []string) ([]string, error) {
	if len(input) > 20 {
		return nil, ErrInvalidInput
	}
	unique := map[string]bool{}
	for _, raw := range input {
		if len(raw) > 128 {
			return nil, ErrInvalidInput
		}
		tag := strings.ToLower(strings.TrimSpace(raw))
		if !tagPattern.MatchString(tag) {
			return nil, ErrInvalidInput
		}
		unique[tag] = true
	}
	result := make([]string, 0, len(unique))
	for tag := range unique {
		result = append(result, tag)
	}
	sort.Strings(result)
	return result, nil
}

// SetTags replaces only the caller's logical-file tags. The existing identity
// lock serializes edits with deletion and revalidates session revocation.
func (s *Store) SetTags(ctx context.Context, id string, input []string) ([]string, error) {
	if _, err := auth.RequireUser(ctx); err != nil {
		return nil, err
	}
	tags, err := NormalizeTags(input)
	if err != nil {
		return nil, err
	}
	if !validID(id) {
		return nil, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, identity, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	var owned string
	err = tx.QueryRow(ctx, "SELECT id::text FROM vault.files WHERE id=$1 AND owner_id=$2", id, identity.UserID).Scan(&owned)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, ErrUnavailable
	}
	if _, err = tx.Exec(ctx, "DELETE FROM vault.file_tags WHERE file_id=$1", id); err != nil {
		return nil, ErrUnavailable
	}
	if len(tags) > 0 {
		if _, err = tx.Exec(ctx, "INSERT INTO vault.file_tags(file_id,tag) SELECT $1::uuid,unnest($2::text[])", id, tags); err != nil {
			return nil, ErrUnavailable
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, ErrUnavailable
	}
	return tags, nil
}
