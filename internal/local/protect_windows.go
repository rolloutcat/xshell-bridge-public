//go:build windows

package local

import (
	"fmt"
	"os/exec"
	"os/user"
)

func Protect(dir string) error {
	u, e := user.Current()
	if e != nil {
		return e
	}
	b, e := exec.Command("icacls.exe", dir, "/inheritance:r", "/grant:r", "*"+u.Uid+":(OI)(CI)F", "*S-1-5-18:(OI)(CI)F").CombinedOutput()
	if e != nil {
		return fmt.Errorf("protect runtime directory: %v: %s", e, b)
	}
	return nil
}
