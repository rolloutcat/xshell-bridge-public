//go:build windows

package mcpserver

import (
	"context"
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// Only the stdio MCP process follows its launcher. An open process handle pins
// identity across PID reuse; a newer creation time means the original is gone.
func parentContext(ctx context.Context) (context.Context, context.CancelFunc) {
	child, cancel := context.WithCancel(ctx)
	pid := os.Getppid()
	if pid <= 0 {
		return child, cancel
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			cancel()
		}
		return child, cancel // Access denied is unknown, never inferred dead.
	}
	var parentCreated, selfCreated, ignored1, ignored2, ignored3 windows.Filetime
	err = windows.GetProcessTimes(h, &parentCreated, &ignored1, &ignored2, &ignored3)
	self := windows.CurrentProcess()
	selfErr := windows.GetProcessTimes(self, &selfCreated, &ignored1, &ignored2, &ignored3)
	if err != nil || selfErr != nil {
		windows.CloseHandle(h)
		return child, cancel
	}
	if parentCreated.Nanoseconds() > selfCreated.Nanoseconds() {
		windows.CloseHandle(h)
		cancel()
		return child, cancel
	}
	go func() {
		defer windows.CloseHandle(h)
		for {
			select {
			case <-child.Done():
				return
			default:
			}
			status, e := windows.WaitForSingleObject(h, uint32((500*time.Millisecond)/time.Millisecond))
			if e != nil {
				return
			}
			if status == windows.WAIT_OBJECT_0 {
				cancel()
				return
			}
		}
	}()
	return child, cancel
}
