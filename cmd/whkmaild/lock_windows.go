//go:build windows

package main

import "os"

// acquireLock is a no-op on Windows; whkmaild is not supported there.
// Returns nil so the caller's defer does not accidentally close os.Stderr.
func acquireLock() (*os.File, error) { return nil, nil }
