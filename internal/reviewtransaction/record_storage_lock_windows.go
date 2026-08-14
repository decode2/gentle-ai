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
	closed    bool
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
	return &RecordStorageLock{authority: authority, data: data}, nil
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
	if err := lockRecordStorageRange(ctx, l.data.handle); err != nil {
		l.mu.Unlock()
		return nil, err
	}
	if err := l.validate(ctx); err != nil {
		_ = unlockRecordStorageRange(l.data.handle)
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
		err := unlockRecordStorageRange(l.data.handle)
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

// Close waits for a lease before releasing the retained handle.
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
	if l.data == nil || l.data.handle == 0 || l.data.Close() != nil {
		return errRecordStorageUnavailable
	}
	return nil
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

func lockRecordStorageRange(ctx context.Context, handle windows.Handle) error {
	event, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		return errRecordStorageUnavailable
	}
	// Completion is consumed before close; an event-close error cannot revoke an acquired range.
	defer windows.CloseHandle(event)
	overlapped := &windows.Overlapped{HEvent: event}
	err = windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, overlapped)
	if err != nil && !errors.Is(err, windows.ERROR_IO_PENDING) {
		return recordStorageLockError(ctx)
	}
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		stop := make(chan struct{})
		finished := make(chan struct{})
		var cancelErr error
		go func() {
			defer close(finished)
			select {
			case <-ctx.Done():
				cancelErr = windows.CancelIoEx(handle, overlapped)
			case <-stop:
			}
		}()
		status, waitErr := windows.WaitForSingleObject(event, windows.INFINITE)
		close(stop)
		<-finished
		var bytes uint32
		if waitErr != nil || status != windows.WAIT_OBJECT_0 {
			if ctx.Err() == nil {
				cancelErr = windows.CancelIoEx(handle, overlapped)
			}
			err = windows.GetOverlappedResult(handle, overlapped, &bytes, true)
			if err == nil {
				_ = unlockRecordStorageRange(handle)
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if cancelErr != nil && !errors.Is(cancelErr, windows.ERROR_NOT_FOUND) {
				return errRecordStorageUnavailable
			}
			return errRecordStorageUnavailable
		}
		err = windows.GetOverlappedResult(handle, overlapped, &bytes, false)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		if err == nil {
			_ = unlockRecordStorageRange(handle)
		}
		return ctxErr
	}
	if err != nil {
		return errRecordStorageUnavailable
	}
	return nil
}

func unlockRecordStorageRange(handle windows.Handle) error {
	// UnlockFileEx matches the byte range and offset, not the acquisition OVERLAPPED.
	return windows.UnlockFileEx(handle, 0, 1, 0, &windows.Overlapped{})
}

func recordStorageLockError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errRecordStorageUnavailable
}
