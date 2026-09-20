package upload

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"time"
)

var temporaryName = regexp.MustCompile(`^(upload-[0-9a-f]{64}\.part|\.candidate-[0-9a-f]{64})$`)

type temporaryDirectory struct {
	root *os.Root
	scan *os.File
}

// TemporaryCleaner walks bounded directory pages, retaining its cursor between
// cycles so active/new entries cannot permanently starve older abandoned files.
// Only exact private temporary names are eligible; final generations are excluded.
type TemporaryCleaner struct {
	mu          sync.Mutex
	directories []temporaryDirectory
	batch       int
	grace       time.Duration
}

func NewTemporaryCleaner(staging, blobs string, batch int) (*TemporaryCleaner, error) {
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		return nil, errors.New("temporary cleanup requires Linux or Windows file locks")
	}
	if staging == "" || blobs == "" || batch < 1 || batch > 100 {
		return nil, ErrInvalidInput
	}
	cleaner := &TemporaryCleaner{batch: batch, grace: time.Hour}
	seen := map[string]bool{}
	for _, directory := range []string{staging, blobs} {
		path, err := filepath.Abs(directory)
		if err != nil {
			_ = cleaner.Close()
			return nil, ErrStorage
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		root, err := os.OpenRoot(path)
		if err != nil {
			_ = cleaner.Close()
			return nil, ErrStorage
		}
		scan, err := root.Open(".")
		if err != nil {
			_ = root.Close()
			_ = cleaner.Close()
			return nil, ErrStorage
		}
		cleaner.directories = append(cleaner.directories, temporaryDirectory{root, scan})
	}
	return cleaner, nil
}

func (c *TemporaryCleaner) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result error
	for _, dir := range c.directories {
		result = errors.Join(result, dir.scan.Close(), dir.root.Close())
	}
	c.directories = nil
	return result
}

func (c *TemporaryCleaner) RunOnce(ctx context.Context) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	removed := 0
	var result error
	for i := range c.directories {
		dir := &c.directories[i]
		entries, err := dir.scan.ReadDir(c.batch * 10)
		if err != nil && !errors.Is(err, io.EOF) {
			result = errors.Join(result, ErrCleanup)
			continue
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return removed, err
			}
			if !temporaryName.MatchString(entry.Name()) {
				continue
			}
			count, err := c.removeAbandoned(dir.root, entry.Name())
			removed += count
			result = errors.Join(result, err)
		}
		if errors.Is(err, io.EOF) || len(entries) == 0 {
			// Opening the replacement first keeps a valid cursor on an I/O failure.
			next, openErr := dir.root.Open(".")
			if openErr != nil {
				result = errors.Join(result, ErrCleanup)
			} else {
				_ = dir.scan.Close()
				dir.scan = next
			}
		}
	}
	return removed, result
}

func (c *TemporaryCleaner) removeAbandoned(root *os.Root, name string) (int, error) {
	before, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, ErrCleanup
	}
	if !before.Mode().IsRegular() || time.Since(before.ModTime()) < c.grace {
		return 0, nil
	}
	file, err := root.OpenFile(name, os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, ErrCleanup
	}
	defer file.Close()
	locked, err := lockTemporary(file)
	if err != nil {
		return 0, ErrCleanup
	}
	if !locked {
		return 0, nil
	}
	// Recheck after acquiring ownership. Never follow a substituted path or use
	// an age observation made before a live writer released its lock.
	info, err := file.Stat()
	if err != nil {
		return 0, ErrCleanup
	}
	current, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, ErrCleanup
	}
	if !current.Mode().IsRegular() || !os.SameFile(info, current) || time.Since(info.ModTime()) < c.grace {
		return 0, nil
	}
	if err = root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, ErrCleanup
	}
	return 1, nil
}
