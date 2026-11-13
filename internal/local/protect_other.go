//go:build !windows

package local

import "os"

func Protect(dir string) error { return os.Chmod(dir, 0700) }
