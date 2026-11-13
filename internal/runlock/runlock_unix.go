//go:build !windows

package runlock

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

func lockFile(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
func processAbsent(pid int) (bool, error) {
	err := unix.Kill(pid, 0)
	if errors.Is(err, unix.ESRCH) {
		return true, nil
	}
	if err == nil || errors.Is(err, unix.EPERM) {
		return false, nil
	}
	return false, err
}
