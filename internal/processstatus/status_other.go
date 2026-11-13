//go:build !windows

package processstatus

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

func Register(string) func() { return func() {} }
func Snapshot() Report {
	return Report{Processes: []Process{}, Note: "Local process status is available on Windows."}
}
