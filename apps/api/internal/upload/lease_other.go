//go:build !linux && !windows

package upload

import "os"

// Other operating systems retain development staging, but cannot run the
// crash sweeper. Production publication already requires Linux.
func lockTemporary(_ *os.File) (bool, error) { return true, nil }
