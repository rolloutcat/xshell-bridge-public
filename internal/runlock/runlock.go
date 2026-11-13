// Package runlock protects a trusted private runtime directory. The directory
// and its ancestors must not be writable by untrusted users. No guard inode is
// ever unlinked; all cooperating new versions lock the same persistent file.
package runlock

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

type Guard struct {
	mu      sync.Mutex
	file    *os.File
	marker  string
	info    os.FileInfo
	content []byte
}

func regular(path string) error {
	i, e := os.Lstat(path)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if !i.Mode().IsRegular() {
		return fmt.Errorf("unsafe lock path: %s", path)
	}
	return nil
}

func Acquire(dir string) (_ *Guard, err error) {
	// Reject symlinks in every existing directory component, not just the files.
	abs, e := filepath.Abs(dir)
	if e != nil {
		return nil, e
	}
	for p := abs; ; p = filepath.Dir(p) {
		i, e := os.Lstat(p)
		if e != nil {
			return nil, e
		}
		if !i.IsDir() || i.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("unsafe runtime directory")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	guardPath := filepath.Join(abs, ".bridge-owner.lock")
	if e = regular(guardPath); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(guardPath, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	defer func() {
		if err != nil {
			_ = f.Close()
		}
	}()
	if e = lockFile(f); e != nil {
		return nil, fmt.Errorf("Bridge 已运行或所有权无法确认；请核查原始 PID，勿删除锁或终止进程: %w", e)
	}
	marker := filepath.Join(abs, "bridge.lock")
	if e = regular(marker); e != nil {
		return nil, e
	}
	if b, e := os.ReadFile(marker); e == nil {
		text := string(b)
		if len(text) > 0 && text[len(text)-1] == '\n' {
			text = text[:len(text)-1]
		}
		valid := len(text) > 0
		for _, c := range text {
			if c < '0' || c > '9' {
				valid = false
			}
		}
		pid, parseErr := strconv.Atoi(text)
		if !valid || parseErr != nil || pid <= 0 {
			return nil, fmt.Errorf("bridge.lock PID 格式异常；保留原标记，请人工核查")
		}
		absent, e := processAbsent(pid)
		if e != nil || !absent {
			return nil, fmt.Errorf("bridge.lock 原始 PID %d 仍存活或无法确认已退出；未修改锁", pid)
		}
		var nonce [16]byte
		if _, e = rand.Read(nonce[:]); e != nil {
			return nil, e
		}
		if e = os.Rename(marker, marker+".stale-"+hex.EncodeToString(nonce[:])); e != nil {
			return nil, e
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	m, e := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return nil, e
	}
	content := []byte(strconv.Itoa(os.Getpid()) + "\n")
	info, e := m.Stat()
	if e == nil {
		_, e = m.Write(content)
	}
	if e == nil {
		e = m.Sync()
	}
	closeErr := m.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		// Failed initialization leaves evidence; it is never mistaken for a live guard.
		return nil, e
	}
	return &Guard{file: f, marker: marker, info: info, content: content}, nil
}

func (g *Guard) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.file == nil {
		return nil
	}
	var err error
	i, e := os.Lstat(g.marker)
	if e == nil && i.Mode().IsRegular() && os.SameFile(i, g.info) {
		b, e := os.ReadFile(g.marker)
		if e != nil {
			err = e
		} else if bytes.Equal(b, g.content) {
			err = os.Remove(g.marker)
		}
	} else if e != nil && !os.IsNotExist(e) {
		err = e
	}
	// Marker checks/removal occur while still owning the OS guard. Within the
	// trusted boundary no cooperating writer can replace it between these steps.
	err = errors.Join(err, g.file.Close())
	g.file = nil
	return err
}
