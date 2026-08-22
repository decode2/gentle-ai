//go:build windows

package filecoord

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

type windowsRuntimeCall struct {
	handle                     windows.Handle
	flags, reserved, low, high uint32
	overlapped                 *windows.Overlapped
}

func windowsRuntimeFile(t *testing.T) *os.File {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "lock-")
	windowsLockTestFatal(t, err)
	return file
}
func windowsRuntimeError(err error) windowsLockFileEx {
	return func(windows.Handle, uint32, uint32, uint32, uint32, *windows.Overlapped) error { return err }
}
func windowsRuntimeAcquire(t *testing.T, target, root string) *Lease {
	lease, err := Acquire(context.Background(), target, root)
	windowsLockTestAssert(t, err == nil && lease != nil, "acquire = lease %v, error %v", lease, err)
	return lease
}

func TestWindowsRuntimeLockFileEx(t *testing.T) {
	file := windowsRuntimeFile(t)
	defer file.Close()
	oldLock, oldWait := nativeWindowsLockFileEx, windowsAcquireWait
	defer func() { nativeWindowsLockFileEx, windowsAcquireWait = oldLock, oldWait }()
	var got windowsRuntimeCall
	nativeWindowsLockFileEx = func(h windows.Handle, f, r, low, high uint32, o *windows.Overlapped) error {
		got = windowsRuntimeCall{h, f, r, low, high, o}
		return nil
	}
	windowsAcquireWait = func(context.Context, time.Duration) error { t.Fatal("unexpected wait"); return nil }
	windowsLockTestFatal(t, lockExclusive(context.Background(), file))
	windowsLockTestAssert(t, got.handle == windows.Handle(file.Fd()) && got.flags == windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY && got.reserved == 0 && got.low == 1 && got.high == 0 && got.overlapped != nil && got.overlapped.Offset == 0 && got.overlapped.OffsetHigh == 0, "LockFileEx arguments = %#v", got)
	cause := errors.New("caller cancelled")
	ctx, cancel := context.WithCancelCause(context.Background())
	nativeWindowsLockFileEx = windowsRuntimeError(windows.ERROR_LOCK_VIOLATION)
	windowsAcquireWait = func(context.Context, time.Duration) error { cancel(cause); return nil }
	err := lockExclusive(ctx, file)
	windowsLockTestAssert(t, errors.Is(err, ErrBusy) && errors.Is(err, cause), "contention error = %v", err)
	op := errors.New("unexpected native failure")
	nativeWindowsLockFileEx = windowsRuntimeError(op)
	err = lockExclusive(context.Background(), file)
	windowsLockTestAssert(t, errors.Is(err, ErrOperational) && errors.Is(err, op) && !errors.Is(err, ErrBusy), "operational error = %v", err)
}

func TestWindowsAcquireLifecycle(t *testing.T) {
	root, target := filepath.Join(t.TempDir(), "root"), filepath.Join(t.TempDir(), "target")
	oldLock, oldUnlock, oldWait, oldClose, oldTimeout := nativeWindowsLockFileEx, nativeWindowsUnlockFileEx, windowsAcquireWait, closeAcquisitionFile, acquireTimeout
	defer func() {
		nativeWindowsLockFileEx, nativeWindowsUnlockFileEx, windowsAcquireWait, closeAcquisitionFile, acquireTimeout = oldLock, oldUnlock, oldWait, oldClose, oldTimeout
	}()
	nativeWindowsLockFileEx = windowsRuntimeError(windows.ERROR_LOCK_VIOLATION)
	acquireTimeout = 5 * time.Millisecond
	windowsAcquireWait = func(ctx context.Context, _ time.Duration) error { <-ctx.Done(); return context.Cause(ctx) }
	caller, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	lease, err := Acquire(caller, target, root)
	windowsLockTestAssert(t, lease == nil && errors.Is(err, ErrBusy) && errors.Is(err, context.DeadlineExceeded), "hard-cap result = lease %v, error %v", lease, err)
	rootErr, fileErr := errors.New("root close"), errors.New("file close")
	var closed []string
	closeAcquisitionFile = func(file *os.File) error {
		name, closeErr := file.Name(), file.Close()
		closed = append(closed, name)
		if filepath.Clean(name) == filepath.Clean(root) {
			return errors.Join(closeErr, rootErr)
		}
		return errors.Join(closeErr, fileErr)
	}
	nativeWindowsLockFileEx = windowsRuntimeError(nil)
	lease, err = Acquire(context.Background(), target, root)
	windowsLockTestAssert(t, lease == nil && errors.Is(err, ErrOperational) && errors.Is(err, rootErr) && errors.Is(err, fileErr) && len(closed) == 2, "cleanup result = lease %v, error %v, closes %v", lease, err, closed)
	var unlockCall windowsRuntimeCall
	var unlocks, fileCloses int
	unlockErr, closeErr := errors.New("unlock"), errors.New("close")
	closeAcquisitionFile = func(file *os.File) error {
		err := file.Close()
		if filepath.Clean(file.Name()) == filepath.Clean(root) {
			return err
		}
		fileCloses++
		if fileCloses == 1 {
			return errors.Join(err, closeErr)
		}
		return err
	}
	nativeWindowsUnlockFileEx = func(h windows.Handle, r, low, high uint32, o *windows.Overlapped) error {
		unlocks++
		unlockCall = windowsRuntimeCall{h, 0, r, low, high, o}
		if unlocks == 1 {
			return unlockErr
		}
		return nil
	}
	lease = windowsRuntimeAcquire(t, target, root)
	wantHandle := windows.Handle(lease.file.Fd())
	first := lease.Release()
	windowsLockTestAssert(t, errors.Is(first, ErrOperational) && errors.Is(first, unlockErr) && errors.Is(first, closeErr) && unlockCall.handle == wantHandle && unlockCall.reserved == 0 && unlockCall.low == 1 && unlockCall.high == 0 && unlockCall.overlapped != nil && unlockCall.overlapped.Offset == 0 && unlockCall.overlapped.OffsetHigh == 0, "release = %v, unlock = %#v", first, unlockCall)
	second := lease.Release()
	windowsLockTestAssert(t, second == first && unlocks == 1 && fileCloses == 1, "second release = %v, unlocks %d, closes %d", second, unlocks, fileCloses)
	windowsRuntimeAcquire(t, target, root).Release()
}

func TestWindowsAcquireSubprocessContention(t *testing.T) {
	if testing.Short() {
		t.Skip("Windows subprocess contention test")
	}
	root, target := filepath.Join(t.TempDir(), "root"), filepath.Join(t.TempDir(), "target")
	cmd := exec.Command(os.Args[0], "-test.run=^TestWindowsLockHelperProcess$")
	cmd.Env = append(os.Environ(), "FILECOORD_WINDOWS_LOCK_HELPER=1", "FILECOORD_LOCK_ROOT="+root, "FILECOORD_LOCK_TARGET="+target)
	stdin, err := cmd.StdinPipe()
	windowsLockTestFatal(t, err)
	stdout, err := cmd.StdoutPipe()
	windowsLockTestFatal(t, err)
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Wait() })
	windowsLockTestFatal(t, cmd.Start())
	_, err = io.ReadFull(stdout, []byte{0})
	windowsLockTestFatal(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	lease, err := Acquire(ctx, target, root)
	windowsLockTestAssert(t, lease == nil && errors.Is(err, ErrBusy) && errors.Is(err, context.DeadlineExceeded), "contender = lease %v, error %v", lease, err)
	_, err = stdin.Write([]byte{1})
	windowsLockTestFatal(t, err)
	windowsLockTestFatal(t, cmd.Wait())
	windowsRuntimeAcquire(t, target, root).Release()
}

func TestWindowsLockHelperProcess(t *testing.T) {
	if os.Getenv("FILECOORD_WINDOWS_LOCK_HELPER") != "1" {
		return
	}
	lease, err := Acquire(context.Background(), os.Getenv("FILECOORD_LOCK_TARGET"), os.Getenv("FILECOORD_LOCK_ROOT"))
	windowsRuntimeHelperExit(err, 2)
	_, err = os.Stdout.Write([]byte{1})
	windowsRuntimeHelperExit(err, 3)
	_, err = io.ReadFull(os.Stdin, []byte{0})
	windowsRuntimeHelperExit(err, 4)
	windowsRuntimeHelperExit(lease.Release(), 5)
}
func windowsRuntimeHelperExit(err error, code int) {
	if err != nil {
		os.Exit(code)
	}
}
