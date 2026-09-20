package admin

import (
	"context"
	"time"

	"file-vault.local/api/internal/files"
	"github.com/jackc/pgx/v5"
)

// SetQuota changes admission policy without deleting content or masking counter drift.
func (s *Store) SetQuota(ctx context.Context, target, value string) (User, error) {
	quota, err := quotaInput(value)
	if err != nil {
		return User{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, actor, err := s.begin(ctx, &target)
	if err != nil {
		return User{}, err
	}
	defer rollback(tx)
	previous, err := readUser(ctx, tx, target)
	if err != nil {
		return User{}, err
	}
	result, err := tx.Exec(ctx, "UPDATE vault.users SET quota_bytes=$2 WHERE id=$1 AND used_bytes<=$2", target, quota)
	if err != nil {
		return User{}, files.ErrUnavailable
	}
	if result.RowsAffected() != 1 {
		return User{}, ErrConflict
	}
	current, err := readUser(ctx, tx, target)
	if err != nil {
		return User{}, err
	}
	if err = writeAudit(ctx, tx, actor.UserID, "USER_QUOTA_CHANGED", previous, current, 0, 0); err != nil {
		return User{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return User{}, files.ErrUnavailable
	}
	return current, nil
}

// SetDisabled cannot disable an administrator. Role assignment and administrative
// account recovery remain operator procedures, preventing self/last-admin lockout.
func (s *Store) SetDisabled(ctx context.Context, target string, disabled bool) (User, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, actor, err := s.begin(ctx, &target)
	if err != nil {
		return User{}, err
	}
	defer rollback(tx)
	previous, err := readUser(ctx, tx, target)
	if err != nil {
		return User{}, err
	}
	if previous.Role == "ADMIN" {
		return User{}, ErrConflict
	}
	action := "USER_ENABLED"
	var sessions, shares int64
	if disabled {
		action = "USER_DISABLED"
		if _, err = tx.Exec(ctx, "UPDATE vault.users SET disabled_at=COALESCE(disabled_at,clock_timestamp()) WHERE id=$1", target); err != nil {
			return User{}, files.ErrUnavailable
		}
		result, err := tx.Exec(ctx, "UPDATE vault.sessions SET revoked_at=clock_timestamp() WHERE user_id=$1 AND revoked_at IS NULL", target)
		if err != nil {
			return User{}, files.ErrUnavailable
		}
		sessions = result.RowsAffected()
		result, err = tx.Exec(ctx, "DELETE FROM vault.file_shares WHERE owner_id=$1", target)
		if err != nil {
			return User{}, files.ErrUnavailable
		}
		shares = result.RowsAffected()
	} else {
		if _, err = tx.Exec(ctx, "UPDATE vault.users SET disabled_at=NULL WHERE id=$1", target); err != nil {
			return User{}, files.ErrUnavailable
		}
	}
	current, err := readUser(ctx, tx, target)
	if err != nil {
		return User{}, err
	}
	if err = writeAudit(ctx, tx, actor.UserID, action, previous, current, sessions, shares); err != nil {
		return User{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return User{}, files.ErrUnavailable
	}
	return current, nil
}
func writeAudit(ctx context.Context, tx pgx.Tx, actor, action string, before, after User, sessions, shares int64) error {
	previousQuota, err := quotaInput(before.QuotaBytes)
	if err != nil {
		return files.ErrInvariant
	}
	newQuota, err := quotaInput(after.QuotaBytes)
	if err != nil {
		return files.ErrInvariant
	}
	_, err = tx.Exec(ctx, `INSERT INTO vault.admin_audit(actor_id,target_user_id,action,previous_quota,new_quota,
 previous_disabled_at,new_disabled_at,revoked_sessions,revoked_shares) VALUES($1,$2,$3,$4::bigint,$5::bigint,$6,$7,$8,$9)`,
		actor, after.ID, action, previousQuota, newQuota, before.DisabledAt, after.DisabledAt, sessions, shares)
	if err != nil {
		return files.ErrUnavailable
	}
	return nil
}
