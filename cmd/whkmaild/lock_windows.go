//go:build windows

package main

import "os"

// acquireLock is a no-op on Windows; whkmaild is not supported there.
func acquireLock() (*os.File, error) { return os.Stderr, nil }
