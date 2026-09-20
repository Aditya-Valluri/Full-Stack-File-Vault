package upload

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"regexp"

	"github.com/jackc/pgx/v5"
)

var ErrRetryConflict = errors.New("upload retry key already used for different content")
var receiptKey = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// The versioned, ordered fingerprint includes display metadata as well as bytes.
// A retry must submit the same files in the same order, including duplicate files.
func uploadFingerprint(files []*Staged) ([32]byte, error) {
	infos := make([]Info, len(files))
	for i, file := range files {
		if file == nil {
			return [32]byte{}, ErrInvalidInput
		}
		infos[i] = file.Info()
	}
	data, err := json.Marshal(struct {
		Version int
		Files   []Info
	}{1, infos})
	if err != nil {
		return [32]byte{}, ErrInvalidInput
	}
	return sha256.Sum256(data), nil
}

// The caller holds the fresh authenticated user's row lock, serializing receipt
// lookup and creation across sessions/replicas before quota is charged.
func loadReceipt(ctx context.Context, tx pgx.Tx, owner, key string, fingerprint [32]byte) ([]PublishedFile, bool, error) {
	var stored, result []byte
	err := tx.QueryRow(ctx, "SELECT fingerprint,result FROM vault.upload_receipts WHERE owner_id=$1 AND request_key=$2", owner, key).Scan(&stored, &result)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, ErrPublication
	}
	if string(stored) != string(fingerprint[:]) {
		return nil, false, ErrRetryConflict
	}
	var files []PublishedFile
	if json.Unmarshal(result, &files) != nil || len(files) == 0 {
		return nil, false, ErrPublication
	}
	return files, true, nil
}

func saveReceipt(ctx context.Context, tx pgx.Tx, owner, key string, fingerprint [32]byte, files []PublishedFile) error {
	data, err := json.Marshal(files)
	if err != nil {
		return ErrPublication
	}
	_, err = tx.Exec(ctx, "INSERT INTO vault.upload_receipts(owner_id,request_key,fingerprint,result) VALUES($1,$2,$3,$4::jsonb)", owner, key, fingerprint[:], string(data))
	if err != nil {
		return ErrPublication
	}
	return nil
}

// ValidRetryKey validates the canonical UUIDv4 format before accepting file parts.
func ValidRetryKey(key string) bool { return receiptKey.MatchString(key) }
