package app

import (
	"errors"
	"syscall"
)

const (
	// processQueryLimitedInformation is PROCESS_QUERY_LIMITED_INFORMATION:
	// enough to read an exit code, granted for most processes.
	processQueryLimitedInformation = 0x1000
	// stillActive is STILL_ACTIVE, the exit code of a running process.
	stillActive = 259
)

// processAlive says whether a process with the id runs. Opening a
// process succeeds while anything holds a handle to it, exited or not,
// so its exit code tells: STILL_ACTIVE while it runs. One that cannot be
// opened for lack of rights runs (as EPERM does on Unix). A process that
// exited with code 259 reads as running: its copies are kept, not lost.
func processAlive(pid int) bool {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return errors.Is(err, syscall.ERROR_ACCESS_DENIED)
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code == stillActive
}
