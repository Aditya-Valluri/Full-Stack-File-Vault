package sharing

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type Activity struct {
	ID, ShareID, Kind, Status string
	RecipientID               *string
	RecipientName             *string
	OccurredAt                time.Time
}

// Only recipient-restricted links attribute identity. A signed-in visitor to an
// unrestricted link remains anonymous to the owner. No peer or device is stored.
// The existing capability lock serializes events for the file's owner; retain a
// bounded tail rather than an unbounded analytics log. File deletion cascades.
func recordActivity(ctx context.Context, tx pgx.Tx, c capability, kind string) error {
	_, err := tx.Exec(ctx, `INSERT INTO vault.share_activity(file_id,share_id,recipient_id,kind,expires_at)
 VALUES($1,$2,$3,$4,$5)`, c.file.ID, c.share.ID, c.share.RecipientID, kind, c.share.ExpiresAt)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM vault.share_activity WHERE file_id=$1 AND id NOT IN
 (SELECT id FROM vault.share_activity WHERE file_id=$1 ORDER BY id DESC LIMIT 100)`, c.file.ID)
	return err
}

func readActivity(ctx context.Context, tx pgx.Tx, fileID string) ([]Activity, error) {
	rows, err := tx.Query(ctx, `SELECT a.id::text,a.share_id::text,a.recipient_id::text,COALESCE(c.login_name, CASE WHEN u.email_verified_at IS NOT NULL AND i.user_id IS NOT NULL THEN u.email_address END),a.kind,a.occurred_at,
 CASE WHEN a.expires_at<=clock_timestamp() THEN 'EXPIRED' WHEN s.id IS NULL THEN 'REVOKED' ELSE 'ACTIVE' END
 FROM vault.share_activity a LEFT JOIN vault.file_shares s ON s.id=a.share_id
 LEFT JOIN vault.users u ON u.id=a.recipient_id
 LEFT JOIN vault.credentials c ON c.user_id=u.id
 LEFT JOIN vault.user_identities i ON i.user_id=u.id AND i.provider='password' AND i.provider_subject=u.email_normalized
 WHERE a.file_id=$1 ORDER BY a.id DESC LIMIT 100`, fileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Activity, 0)
	for rows.Next() {
		var a Activity
		if err = rows.Scan(&a.ID, &a.ShareID, &a.RecipientID, &a.RecipientName, &a.Kind, &a.OccurredAt, &a.Status); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}
