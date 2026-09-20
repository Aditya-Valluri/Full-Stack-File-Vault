package config

import (
	"errors"
	"os"
	"strconv"
	"time"

	"file-vault.local/api/internal/graph"
)

func uploadConfig() (graph.MultipartConfig, string, int, error) {
	c := graph.MultipartConfig{Directory: os.Getenv("UPLOAD_STAGING_DIR"), Timeout: 20 * time.Second}
	dir := os.Getenv("BLOB_STORAGE_DIR")
	if c.Directory == "" {
		c.Directory = "data/staging"
	}
	if dir == "" {
		dir = "data/blobs"
	}
	values := []struct {
		name              string
		fallback, ceiling int64
	}{
		{"UPLOAD_MAX_FILE_BYTES", 20000000, 1 << 30},
		{"UPLOAD_MAX_REQUEST_BYTES", 21000000, 2 << 30},
		{"UPLOAD_MAX_FILES", 10, 100},
		{"UPLOAD_MAX_CONCURRENT", 4, 128},
		{"USER_CALLS_PER_SECOND", 2, 100},
	}
	parsed := make([]int64, len(values))
	for i, v := range values {
		parsed[i] = v.fallback
		if raw := os.Getenv(v.name); raw != "" {
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || n < 1 || n > v.ceiling {
				return c, "", 0, errors.New(v.name + " is outside its supported range")
			}
			parsed[i] = n
		}
	}
	c.MaxFileBytes = parsed[0]
	c.MaxRequestBytes = parsed[1]
	c.MaxFiles = int(parsed[2])
	c.MaxConcurrentRequests = int(parsed[3])
	if c.MaxRequestBytes <= c.MaxFileBytes {
		return c, "", 0, errors.New("UPLOAD_MAX_REQUEST_BYTES must exceed UPLOAD_MAX_FILE_BYTES for multipart overhead")
	}
	return c, dir, int(parsed[4]), nil
}
