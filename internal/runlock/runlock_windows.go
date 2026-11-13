package runlock

import (
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"os"
)

func lockFile(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
}
func processAbsent(pid int) (bool, error) {
	if uint64(pid) > uint64(^uint32(0)) {
		return false, fmt.Errorf("PID out of range")
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err = windows.GetExitCodeProcess(h, &code); err != nil {
		return false, err
	}
	return code != 259, nil // STILL_ACTIVE
}
