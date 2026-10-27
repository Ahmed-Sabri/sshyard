//go:build !windows

package app

import (
	"errors"
	"syscall"
)

// processAlive says whether a process with the id runs.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
