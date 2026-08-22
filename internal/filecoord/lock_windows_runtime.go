//go:build windows

package filecoord

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

type windowsLockFileEx func(windows.Handle, uint32, uint32, uint32, uint32, *windows.Overlapped) error
type windowsUnlockFileEx func(windows.Handle, uint32, uint32, uint32, *windows.Overlapped) error

var (
	nativeWindowsLockFileEx   windowsLockFileEx   = windows.LockFileEx
	nativeWindowsUnlockFileEx windowsUnlockFileEx = windows.UnlockFileEx
	acquireTimeout                                = 30 * time.Second
	acquirePollInterval                           = 10 * time.Millisecond
	windowsAcquireWait                            = waitWindowsAcquire
)

func acquireBackend(ctx context.Context, target, lockRoot string) (*Lease, error) {
	lockPath, err := LockPath(lockRoot, target)
	if err != nil {
		return nil, err
	}
	root, err := openPrivateRoot(filepath.Dir(lockPath))
	if err != nil {
		return nil, err
	}
	sid, err := currentWindowsLockSID()
	if err != nil {
		return nil, closeAcquisitionFailure(&OperationalError{Cause: err}, root)
	}
	file, err := openLockFile(root, filepath.Base(lockPath), sid)
	if err != nil {
		return nil, closeAcquisitionFailure(err, root)
	}
	if err := closeAcquisitionFile(root); err != nil {
		return nil, closeAcquisitionFailure(&OperationalError{Cause: err}, file)
	}
	bounded, cancel := context.WithTimeout(ctx, acquireTimeout)
	defer cancel()
	if err := lockExclusive(bounded, file); err != nil {
		return nil, closeAcquisitionFailure(err, file)
	}
	return &Lease{file: file, unlock: unlockExclusive, close: func() error {
		return closeAcquisitionFile(file)
	}}, nil
}

func lockExclusive(ctx context.Context, file *os.File) error {
	for {
		if ctx.Err() != nil {
			return busyContextError(ctx)
		}
		err := nativeWindowsLockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, new(windows.Overlapped))
		if err == nil {
			return nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return &OperationalError{Cause: err}
		}
		if err := windowsAcquireWait(ctx, acquirePollInterval); err != nil {
			return busyContextError(ctx)
		}
	}
}

func busyContextError(ctx context.Context) error {
	cause := context.Cause(ctx)
	if cause == nil {
		cause = ctx.Err()
	}
	return &BusyError{Cause: cause}
}

func waitWindowsAcquire(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func unlockExclusive(file *os.File) error {
	return nativeWindowsUnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, new(windows.Overlapped))
}
