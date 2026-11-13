//go:build windows

// Package processstatus reports best-effort local process metadata. Its ephemeral
// per-process events contain no configuration, commands or credentials.
package processstatus

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type Process struct {
	PID          uint32 `json:"pid"`
	ParentPID    uint32 `json:"parent_pid"`
	ParentStatus string `json:"parent_status"`
	Role         string `json:"role"`
}
type Report struct {
	Processes []Process `json:"processes"`
	Note      string    `json:"note"`
	Truncated bool      `json:"truncated"`
	Available bool      `json:"available"`
}

func stamp(h windows.Handle) (uint64, error) {
	var a, b, c, d windows.Filetime
	err := windows.GetProcessTimes(h, &a, &b, &c, &d)
	return uint64(a.HighDateTime)<<32 | uint64(a.LowDateTime), err
}
func executable(h windows.Handle) (string, error) {
	b := make([]uint16, 32768)
	n := uint32(len(b))
	if err := windows.QueryFullProcessImageName(h, 0, &b[0], &n); err != nil {
		return "", err
	}
	return filepath.Clean(windows.UTF16ToString(b[:n])), nil
}
func eventName(role string, pid uint32, start uint64, path string) string {
	identity := sha256.Sum256([]byte(strings.ToUpper(path)))
	return fmt.Sprintf("Local\\XshellBridge-role-%s-%d-%016x-%x", role, pid, start, identity[:12])
}
func Register(role string) func() {
	if role != "bridge" && role != "mcp" && role != "cli" {
		return func() {}
	}
	start, err := stamp(windows.CurrentProcess())
	if err != nil {
		return func() {}
	}
	path, err := executable(windows.CurrentProcess())
	if err != nil {
		return func() {}
	}
	name, err := windows.UTF16PtrFromString(eventName(role, uint32(os.Getpid()), start, path))
	if err != nil {
		return func() {}
	}
	h, err := windows.CreateEvent(nil, 1, 0, name)
	if err != nil {
		return func() {}
	}
	return func() { windows.CloseHandle(h) }
}
func roleFor(pid uint32, start uint64, path string) string {
	for _, role := range []string{"bridge", "mcp", "cli"} {
		name, _ := windows.UTF16PtrFromString(eventName(role, pid, start, path))
		h, err := windows.OpenEvent(windows.SYNCHRONIZE, false, name)
		if err == nil {
			windows.CloseHandle(h)
			return role
		}
	}
	return "unknown"
}
func parentStatus(pid uint32, childStart uint64) string {
	if pid == 0 {
		return "unknown"
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		if err == windows.ERROR_INVALID_PARAMETER {
			return "exited"
		}
		return "unknown"
	}
	defer windows.CloseHandle(h)
	created, err := stamp(h)
	if err != nil {
		return "unknown"
	}
	if created > childStart {
		return "exited"
	}
	result, err := windows.WaitForSingleObject(h, 0)
	if err != nil {
		return "unknown"
	}
	if result == windows.WAIT_OBJECT_0 {
		return "exited"
	}
	if result == uint32(windows.WAIT_TIMEOUT) {
		return "alive"
	}
	return "unknown"
}
func Snapshot() Report {
	report := Report{Processes: []Process{}, Note: "角色由当前版本进程自报；旧版或无法访问时为 unknown。多个 MCP 可分别服务不同客户端；后台 Bridge 的父进程退出可能正常。此诊断不停止进程。"}
	exe, err := os.Executable()
	if err != nil {
		return report
	}
	name := filepath.Base(exe)
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return report
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	err = windows.Process32First(snap, &e)
	if err != nil {
		return report
	}
	report.Available = true
	scanned := 0
	for err == nil {
		scanned++
		if scanned > 32768 || len(report.Processes) >= 512 {
			report.Truncated = true
			break
		}
		if strings.EqualFold(windows.UTF16ToString(e.ExeFile[:]), name) {
			row := Process{PID: e.ProcessID, ParentPID: e.ParentProcessID, ParentStatus: "unknown", Role: "unknown"}
			h, openErr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, e.ProcessID)
			if openErr == nil {
				start, stampErr := stamp(h)
				if stampErr == nil {
					path, pathErr := executable(h)
					if pathErr == nil {
						row.Role = roleFor(e.ProcessID, start, path)
					}
					row.ParentStatus = parentStatus(e.ParentProcessID, start)
				}
				windows.CloseHandle(h)
			}
			report.Processes = append(report.Processes, row)
		}
		err = windows.Process32Next(snap, &e)
	}
	return report
}
