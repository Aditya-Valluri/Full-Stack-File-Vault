package sharing

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type Activity struct {
	ID, ShareID, Kind, Status string
	RecipientID               *string
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
	rows, err := tx.Query(ctx, `SELECT a.id::text,a.share_id::text,a.recipient_id::text,a.kind,a.occurred_at,
 CASE WHEN a.expires_at<=clock_timestamp() THEN 'EXPIRED' WHEN s.id IS NULL THEN 'REVOKED' ELSE 'ACTIVE' END
 FROM vault.share_activity a LEFT JOIN vault.file_shares s ON s.id=a.share_id
 WHERE a.file_id=$1 ORDER BY a.id DESC LIMIT 100`, fileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Activity, 0)
	for rows.Next() {
		var a Activity
		if err = rows.Scan(&a.ID, &a.ShareID, &a.RecipientID, &a.Kind, &a.OccurredAt, &a.Status); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}
