// Package secret reads explicitly configured secrets without logging them.
package secret

import (
	"errors"
	"io"
	"os"
	"strings"
)

func Read(name string) (string, error) {
	value, path := os.Getenv(name), os.Getenv(name+"_FILE")
	if value != "" && path != "" {
		return "", errors.New(name + " has conflicting sources")
	}
	if path == "" {
		return value, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", errors.New(name + " secret unavailable")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New(name + " secret unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil || len(data) > 16384 {
		return "", errors.New(name + " secret unavailable")
	}
	value = strings.TrimSpace(string(data))
	if value == "" {
		return "", errors.New(name + " secret is empty")
	}
	return value, nil
}
