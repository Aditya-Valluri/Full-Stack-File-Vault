package upload

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

// Locks survive slow I/O and are released by the kernel when a process exits.
func lockTemporary(file *os.File) (bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return false, nil
	}
	return err == nil, err
}
