// Package admin implements narrowly scoped, transaction-authorized administration.
package admin

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"time"

	"full-stack-file-vault.local/api/internal/auth"
	"full-stack-file-vault.local/api/internal/files"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrForbidden = errors.New("administrator role required")
	ErrConflict  = errors.New("administrative change conflicts with current state")
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, files.ErrUnavailable
	}
	return &Store{pool}, nil
}

var uuidPattern = regexp.MustCompile("^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")

func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

// begin locks every participating user in the same UUID order as sharing, then
// locks/rechecks the actor's session and CURRENT role. No client role is trusted.
func (s *Store) begin(ctx context.Context, target *string) (pgx.Tx, auth.Session, error) {
	identity, err := auth.RequireUser(ctx)
	if err != nil {
		return nil, identity, err
	}
	if target != nil && !uuidPattern.MatchString(*target) {
		return nil, identity, files.ErrInvalidInput
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, identity, files.ErrUnavailable
	}
	ok := false
	defer func() {
		if !ok {
			rollback(tx)
		}
	}()
	if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout='2s'; SET LOCAL statement_timeout='3s'"); err != nil {
		return nil, identity, files.ErrUnavailable
	}
	users := []string{identity.UserID}
	if target != nil && *target != identity.UserID {
		users = append(users, *target)
	}
	rows, err := tx.Query(ctx, "SELECT id::text FROM vault.users WHERE id=ANY($1::uuid[]) ORDER BY id FOR UPDATE", users)
	if err != nil {
		return nil, identity, files.ErrUnavailable
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, identity, files.ErrUnavailable
		}
	}
	rows.Close()
	if rows.Err() != nil {
		return nil, identity, files.ErrUnavailable
	}
	identity, err = auth.LockPublicationIdentity(ctx, tx)
	if err != nil {
		return nil, identity, err
	}
	if identity.Role != "ADMIN" {
		return nil, identity, ErrForbidden
	}
	ok = true
	return tx, identity, nil
}

type User struct {
	ID, Role, UsedBytes, QuotaBytes string
	LoginName                       *string
	DisabledAt                      *time.Time
	CreatedAt                       time.Time
}
type UserPage struct {
	Nodes       []User
	EndCursor   *string
	HasNextPage bool
}
type File struct {
	File           files.File
	OwnerID        string
	LoginName      *string
	DownloadStarts string
}
type FilePage struct {
	Nodes       []File
	EndCursor   *string
	HasNextPage bool
}
type Statistics struct{ UserCount, FileCount, LogicalBytes, ReferencedBytes, PendingDeletionBytes, SavedBytes, SavingsPercent, DownloadStarts string }
type Audit struct {
	ID, ActorID, TargetUserID, Action, PreviousQuota, NewQuota, RevokedSessions, RevokedShares string
	OccurredAt                                                                                 time.Time
	PreviousDisabledAt, NewDisabledAt                                                          *time.Time
}
type AuditPage struct {
	Nodes       []Audit
	EndCursor   *string
	HasNextPage bool
}

func pageInput(first int, after *string) error {
	if first < 1 || first > 50 || (after != nil && !uuidPattern.MatchString(*after)) {
		return files.ErrInvalidInput
	}
	return nil
}
func quotaInput(value string) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 || strconv.FormatInt(parsed, 10) != value {
		return 0, files.ErrInvalidInput
	}
	return parsed, nil
}

const userColumns = "u.id::text,c.login_name,u.role,u.used_bytes::text,u.quota_bytes::text,u.disabled_at,u.created_at"

func scanUser(row pgx.Row) (User, error) {
	var user User
	err := row.Scan(&user.ID, &user.LoginName, &user.Role, &user.UsedBytes, &user.QuotaBytes, &user.DisabledAt, &user.CreatedAt)
	return user, err
}
func readUser(ctx context.Context, tx pgx.Tx, id string) (User, error) {
	user, err := scanUser(tx.QueryRow(ctx, "SELECT "+userColumns+" FROM vault.users u LEFT JOIN vault.credentials c ON c.user_id=u.id WHERE u.id=$1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, files.ErrNotFound
	}
	if err != nil {
		return User{}, files.ErrUnavailable
	}
	return user, nil
}
