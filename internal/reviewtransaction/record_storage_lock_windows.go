//go:build windows

package reviewtransaction

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const recordStorageLockName = ".record.lock"

// RecordStorageLock serializes record mutations for one retained authority.
// The backend lifecycle must close locks before closing their authority; an
// authority is not independently kept alive while a lease is held.
type RecordStorageLock struct {
	mu        sync.Mutex
	authority *RecordStorageAuthority
	data      *secureWindowsData
	ops       recordStorageLockOps
	closeData func(*secureWindowsData) error
	closed    bool
}

// recordStorageLockOps stays per lock so one lock's native completion cannot
// affect another lock's lifecycle.
type recordStorageLockOps struct {
	createEvent func() (windows.Handle, error)
	closeEvent  func(windows.Handle) error
	lock        func(windows.Handle, uint32, uint32, uint32, uint32, *windows.Overlapped) error
	cancel      func(windows.Handle, *windows.Overlapped) error
	wait        func(windows.Handle, uint32) (uint32, error)
	result      func(windows.Handle, *windows.Overlapped, *uint32, bool) error
	unlock      func(windows.Handle, uint32, uint32, uint32, *windows.Overlapped) error
}

func newRecordStorageLockOps() recordStorageLockOps {
	return recordStorageLockOps{
		createEvent: func() (windows.Handle, error) { return windows.CreateEvent(nil, 0, 0, nil) },
		closeEvent:  windows.CloseHandle,
		lock:        windows.LockFileEx,
		cancel:      windows.CancelIoEx,
		wait:        windows.WaitForSingleObject,
		result:      windows.GetOverlappedResult,
		unlock:      windows.UnlockFileEx,
	}
}

// OpenRecordStorageLock opens the private, retained lock file for an authority.
func OpenRecordStorageLock(ctx context.Context, authority *RecordStorageAuthority) (*RecordStorageLock, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if authority == nil {
		return nil, errRecordStorageAuthorityInvalid
	}
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if err := authority.validateLocked(ctx); err != nil {
		return nil, err
	}
	data, err := createRecordStorageLock(ctx, authority.digest.handle, &authority.digest.id)
	if errors.Is(err, errSecureWindowsChildExists) {
		data, err = openRecordStorageLock(ctx, authority.digest.handle, &authority.digest.id)
	}
	if err != nil || !validRecordStorageLock(data) {
		if data != nil {
			_ = data.Close()
		}
		return nil, recordStorageLockError(ctx)
	}
	return &RecordStorageLock{authority: authority, data: data, ops: newRecordStorageLockOps(), closeData: func(data *secureWindowsData) error { return data.Close() }}, nil
}

// Lock retains the local mutex until its single-use release function runs.
func (l *RecordStorageLock) Lock(ctx context.Context) (func() error, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l == nil {
		return nil, errRecordStorageUnavailable
	}
	l.mu.Lock()
	if l.closed || l.authority == nil || !validRecordStorageLock(l.data) {
		l.mu.Unlock()
		return nil, errRecordStorageUnavailable
	}
	if err := l.validate(ctx); err != nil {
		l.mu.Unlock()
		return nil, err
	}
	ops := l.ops
	result := lockRecordStorageRange(ctx, l.data.handle, ops)
	if result.uncertain {
		l.poisonLocked()
		l.mu.Unlock()
		return nil, result.err
	}
	if result.err != nil {
		l.mu.Unlock()
		return nil, result.err
	}
	if err := l.validate(ctx); err != nil {
		if unlockRecordStorageRange(l.data.handle, ops) != nil {
			l.poisonLocked()
			err = recordStorageLockError(ctx)
		}
		l.mu.Unlock()
		return nil, err
	}
	used := false
	var token sync.Mutex
	return func() error {
		token.Lock()
		defer token.Unlock()
		if used {
			return errRecordStorageUnavailable
		}
		used = true
		err := unlockRecordStorageRange(l.data.handle, ops)
		if err != nil {
			l.poisonLocked()
		}
		l.mu.Unlock()
		if err != nil {
			return errRecordStorageUnavailable
		}
		return nil
	}, nil
}

func (l *RecordStorageLock) validate(ctx context.Context) error {
	if l == nil || l.authority == nil || !validRecordStorageLock(l.data) {
		return errRecordStorageUnavailable
	}
	l.authority.mu.Lock()
	defer l.authority.mu.Unlock()
	if err := l.authority.validateLocked(ctx); err != nil {
		return err
	}
	opened, err := openRecordStorageLock(ctx, l.authority.digest.handle, &l.authority.digest.id)
	if err != nil || !validRecordStorageLock(opened) || opened.id != l.data.id {
		if opened != nil {
			_ = opened.Close()
		}
		return recordStorageLockError(ctx)
	}
	if opened.Close() != nil {
		return errRecordStorageUnavailable
	}
	return nil
}

// Close waits for a lease before releasing the retained handle. closed is
// committed first because CloseHandle has an uncertain, non-retryable outcome.
func (l *RecordStorageLock) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	if l.data == nil || l.data.handle == 0 || l.closeLocked() != nil {
		return errRecordStorageUnavailable
	}
	return nil
}

func (l *RecordStorageLock) poisonLocked() {
	l.closed = true
	_ = l.closeLocked()
}

func (l *RecordStorageLock) closeLocked() error {
	if l.data == nil || l.data.handle == 0 {
		return nil
	}
	if l.closeData != nil {
		return l.closeData(l.data)
	}
	return l.data.Close()
}

func createRecordStorageLock(ctx context.Context, parent windows.Handle, want *secureWindowsChildID) (*secureWindowsData, error) {
	return openRecordStorageLockFile(ctx, parent, want, windows.FILE_CREATE, true)
}

func openRecordStorageLock(ctx context.Context, parent windows.Handle, want *secureWindowsChildID) (*secureWindowsData, error) {
	return openRecordStorageLockFile(ctx, parent, want, windows.FILE_OPEN, false)
}

func openRecordStorageLockFile(ctx context.Context, parent windows.Handle, want *secureWindowsChildID, disposition uint32, create bool) (*secureWindowsData, error) {
	if err := ctx.Err(); err != nil || !secureWindowsDirectoryHandle(parent, want) {
		return nil, recordStorageLockError(ctx)
	}
	name, err := windows.NewNTUnicodeString(recordStorageLockName)
	if err != nil {
		return nil, errSecureWindowsChildInvalid
	}
	attributes := &windows.OBJECT_ATTRIBUTES{Length: uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})), RootDirectory: parent, ObjectName: name, Attributes: windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE}
	if create {
		descriptor, descriptorErr := ownerOnlyRARSecurityDescriptor(false)
		if descriptorErr != nil {
			return nil, errSecureWindowsChildInvalid
		}
		attributes.SecurityDescriptor = descriptor
		defer runtime.KeepAlive(descriptor)
	}
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&handle, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.READ_CONTROL|windows.SYNCHRONIZE, attributes, &status, nil, windows.FILE_ATTRIBUTE_NORMAL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, disposition, windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT, 0, 0)
	if err != nil {
		return nil, secureWindowsChildError(err)
	}
	data := &secureWindowsData{handle: handle}
	info, _, ok := secureWindowsDataInfo(handle)
	if !secureWindowsDirectoryHandle(parent, want) || !ok || !validRecordStorageLock(data) || info.FileSizeHigh != 0 || info.FileSizeLow != 0 {
		_ = data.Close()
		return nil, errSecureWindowsChildInvalid
	}
	return data, nil
}

func validRecordStorageLock(data *secureWindowsData) bool {
	return data != nil && data.valid() && localWindowsDiskHandle(data.handle)
}

type recordStorageLockRangeResult struct {
	err       error
	uncertain bool
}

func lockRecordStorageRange(ctx context.Context, handle windows.Handle, ops recordStorageLockOps) recordStorageLockRangeResult {
	event, err := ops.createEvent()
	if err != nil {
		return recordStorageLockRangeResult{err: errRecordStorageUnavailable}
	}
	overlapped := &windows.Overlapped{HEvent: event}
	finish := func(primary error, acquired bool) recordStorageLockRangeResult {
		if ops.closeEvent(event) != nil && acquired {
			return clearRecordStorageLockRange(handle, ops, errRecordStorageUnavailable)
		}
		return recordStorageLockRangeResult{err: primary}
	}
	err = ops.lock(handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, overlapped)
	if err != nil && !errors.Is(err, windows.ERROR_IO_PENDING) {
		return finish(recordStorageLockError(ctx), false)
	}
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		stop := make(chan struct{})
		finished := make(chan struct{})
		var cancelErr error
		go func() {
			defer close(finished)
			select {
			case <-ctx.Done():
				cancelErr = ops.cancel(handle, overlapped)
			case <-stop:
			}
		}()
		status, waitErr := ops.wait(event, windows.INFINITE)
		close(stop)
		<-finished
		var bytes uint32
		if waitErr != nil || status != windows.WAIT_OBJECT_0 {
			if ctx.Err() == nil {
				cancelErr = ops.cancel(handle, overlapped)
			}
			err = ops.result(handle, overlapped, &bytes, true)
			if ctx.Err() != nil {
				if err == nil || !errors.Is(err, windows.ERROR_OPERATION_ABORTED) {
					result := clearRecordStorageLockRange(handle, ops, ctx.Err())
					return finish(result.err, false)
				}
				return finish(ctx.Err(), false)
			}
			if err == nil || !errors.Is(err, windows.ERROR_OPERATION_ABORTED) {
				result := clearRecordStorageLockRange(handle, ops, errRecordStorageUnavailable)
				return finish(result.err, false)
			}
			_ = cancelErr // Completion, not cancellation, determines ownership.
			return finish(errRecordStorageUnavailable, false)
		}
		// Cancellation is authoritative only after the exact request's completion
		// has been consumed. ERROR_NOT_FOUND only means completion won the race.
		err = ops.result(handle, overlapped, &bytes, false)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		if err == nil {
			result := clearRecordStorageLockRange(handle, ops, ctxErr)
			return finish(result.err, false)
		} else if errors.Is(err, windows.ERROR_OPERATION_ABORTED) {
			return finish(ctxErr, false)
		} else {
			result := clearRecordStorageLockRange(handle, ops, ctxErr)
			return finish(result.err, false)
		}
	}
	if err != nil {
		if errors.Is(err, windows.ERROR_OPERATION_ABORTED) {
			return finish(errRecordStorageUnavailable, false)
		}
		result := clearRecordStorageLockRange(handle, ops, errRecordStorageUnavailable)
		return finish(result.err, false)
	}
	return finish(nil, true)
}

func clearRecordStorageLockRange(handle windows.Handle, ops recordStorageLockOps, primary error) recordStorageLockRangeResult {
	if unlockRecordStorageRange(handle, ops) != nil {
		return recordStorageLockRangeResult{err: primary, uncertain: true}
	}
	return recordStorageLockRangeResult{err: primary}
}

func unlockRecordStorageRange(handle windows.Handle, ops recordStorageLockOps) error {
	// UnlockFileEx matches the byte range and offset, not the acquisition OVERLAPPED.
	return ops.unlock(handle, 0, 1, 0, &windows.Overlapped{})
}

func recordStorageLockError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errRecordStorageUnavailable
}
