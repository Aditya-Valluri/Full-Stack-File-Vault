package upload

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
)

func lockTemporary(file *os.File) (bool, error) {
	// Lock one byte far beyond any accepted upload. Windows byte locks are
	// mandatory, so locking content bytes would interfere with independent readers.
	position := windows.Overlapped{Offset: 0xfffffffe, OffsetHigh: 0x7fffffff}
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &position)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}
