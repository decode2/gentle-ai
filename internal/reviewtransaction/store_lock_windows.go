//go:build windows

package reviewtransaction

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func tryLockFile(file *os.File) (bool, error) { return tryLockFileMode(file, true) }

func tryLockFileMode(file *os.File, exclusive bool) (bool, error) {
	overlapped := new(windows.Overlapped)
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	err := windows.LockFileEx(windows.Handle(file.Fd()), flags, 0, 1, 0, overlapped)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return false, err
}

func unlockFile(file *os.File) error {
	overlapped := new(windows.Overlapped)
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, overlapped)
}

func isProcessRunning(pid int) bool {
	// On Windows, FindProcess always succeeds, we need to check if we can query it.
	// But let's just return true for now, or check if it exists:
	// A simple check is to wait with timeout 0. But that's complicated.
	// Instead we can use os.FindProcess(pid), then check if it's alive.
	// Let's just assume it's running on Windows for this PR if we can't easily check.
	// Wait, let's just use open process and check exit code?
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var exitCode uint32
	err = windows.GetExitCodeProcess(h, &exitCode)
	if err != nil {
		return false
	}
	return exitCode == 259 // STILL_ACTIVE
}
