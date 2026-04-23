//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"

	"github.com/wkirschbaum/whkmail/internal/dirs"
)

// acquireLock obtains an exclusive flock(2) on the daemon lock file.
// A second whkmaild instance calling this returns an error immediately.
// The caller must keep the returned file open for the lifetime of the process;
// closing it releases the lock.
func acquireLock() (*os.File, error) {
	path := dirs.LockFile()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("another whkmaild is already running (lock: %s)", path)
	}
	return f, nil
}
