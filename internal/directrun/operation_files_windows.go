//go:build windows

package directrun

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unsafe"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
	"golang.org/x/sys/windows"
)

// windowsOperationFiles retains a root handle. Paths are only ever resolved
// from that handle; the configured spelling is not reused as authority.
type windowsOperationFiles struct {
	mu      sync.Mutex
	lease   *reviewtransaction.RepositoryIdentityLease
	root    windows.Handle
	rootID  windowsFileID
	allowed []windowsAllowedRoot
	closed  bool
	ops     windowsOperationFileOps
}

type windowsAllowedRoot struct {
	logical string
	handle  windows.Handle
	id      windowsFileID
}

type windowsFileID struct{ volume, high, low uint32 }

// windowsDestinationProof is captured from the retained target handle and is
// checked again from a no-follow relative handle at the rename boundary.
type windowsDestinationProof struct {
	id        windowsFileID
	digest    Digest
	owner     string
	dacl      []byte
	protected bool
	attrs     uint32
}

type windowsOperationFileOps struct {
	open        func(windows.Handle, string, bool, uint32) (windows.Handle, error)
	info        func(windows.Handle) (windows.ByHandleFileInformation, error)
	read        func(windows.Handle, []byte, *uint32) error
	write       func(windows.Handle, []byte, *uint32) error
	flush       func(windows.Handle) error
	security    func(windows.Handle, windows.SECURITY_INFORMATION) (*windows.SECURITY_DESCRIPTOR, error)
	setSecurity func(windows.Handle, windows.SECURITY_INFORMATION, *windows.SID, *windows.ACL) error
	createEvent func() (windows.Handle, error)
	closeEvent  func(windows.Handle) error
	lock        func(windows.Handle, uint32, uint32, uint32, uint32, *windows.Overlapped) error
	cancel      func(windows.Handle, *windows.Overlapped) error
	wait        func(windows.Handle, uint32) (uint32, error)
	result      func(windows.Handle, *windows.Overlapped, *uint32, bool) error
	unlock      func(windows.Handle, uint32, uint32, uint32, *windows.Overlapped) error
	publish     func(windows.Handle, windows.Handle, string, windowsDestinationProof) error
	cleanup     func(windows.Handle) error
	enumerate   func(windows.Handle) ([]os.DirEntry, error)
	close       func(windows.Handle) error
}

func newWindowsOperationFileOps() windowsOperationFileOps {
	return windowsOperationFileOps{
		open: openWindowsRelative,
		info: func(h windows.Handle) (windows.ByHandleFileInformation, error) {
			var info windows.ByHandleFileInformation
			return info, windows.GetFileInformationByHandle(h, &info)
		},
		read:  func(h windows.Handle, data []byte, done *uint32) error { return windows.ReadFile(h, data, done, nil) },
		write: func(h windows.Handle, data []byte, done *uint32) error { return windows.WriteFile(h, data, done, nil) },
		flush: windows.FlushFileBuffers,
		security: func(h windows.Handle, mask windows.SECURITY_INFORMATION) (*windows.SECURITY_DESCRIPTOR, error) {
			return windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, mask)
		},
		setSecurity: func(h windows.Handle, mask windows.SECURITY_INFORMATION, owner *windows.SID, dacl *windows.ACL) error {
			return windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT, mask, owner, nil, dacl, nil)
		},
		createEvent: func() (windows.Handle, error) { return windows.CreateEvent(nil, 0, 0, nil) },
		closeEvent:  windows.CloseHandle,
		lock:        windows.LockFileEx, cancel: windows.CancelIoEx, wait: windows.WaitForSingleObject, result: windows.GetOverlappedResult, unlock: windows.UnlockFileEx,
		publish: windowsRenameRelative, cleanup: windowsDelete,
		enumerate: func(h windows.Handle) ([]os.DirEntry, error) {
			var duplicate windows.Handle
			if err := windows.DuplicateHandle(windows.CurrentProcess(), h, windows.CurrentProcess(), &duplicate, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
				return nil, err
			}
			file := os.NewFile(uintptr(duplicate), "")
			defer file.Close()
			return file.ReadDir(-1)
		},
		close: windows.CloseHandle,
	}
}

func newPlatformOperationFiles(ctx context.Context, lease *reviewtransaction.RepositoryIdentityLease, handoff Handoff) (operationFiles, error) {
	if lease == nil || handoff.Validate() != nil || lease.Validate(ctx) != nil {
		return nil, ErrOperationUnavailable
	}
	root, err := openWindowsRoot(lease.Identity().RepositoryRoot)
	if err != nil {
		return nil, ErrOperationUnavailable
	}
	f := &windowsOperationFiles{lease: lease, root: root, ops: newWindowsOperationFileOps()}
	fail := func() (operationFiles, error) {
		for i := len(f.allowed) - 1; i >= 0; i-- {
			_ = f.ops.close(f.allowed[i].handle)
		}
		_ = f.ops.close(root)
		return nil, ErrOperationUnavailable
	}
	info, err := f.ops.info(root)
	if err != nil || !windowsDirectory(info) || !windowsCurrentOwner(root) || !windowsLocalDisk(root) {
		return fail()
	}
	f.rootID = windowsIdentity(info)
	for _, configured := range handoff.AllowedEditRoots {
		logical, ok := logicalEditRootWindows(lease.Identity().RepositoryRoot, configured)
		if !ok {
			return fail()
		}
		dir, err := f.walk(logical)
		if err != nil {
			return fail()
		}
		info, statErr := f.ops.info(dir)
		if statErr != nil || !windowsDirectory(info) {
			_ = f.ops.close(dir)
			return fail()
		}
		f.allowed = append(f.allowed, windowsAllowedRoot{logical: logical, handle: dir, id: windowsIdentity(info)})
	}
	return f, nil
}

func (f *windowsOperationFiles) Close() error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	err := error(nil)
	for i := len(f.allowed) - 1; i >= 0; i-- {
		if f.ops.close(f.allowed[i].handle) != nil {
			err = ErrOperationUnavailable
		}
	}
	if f.ops.close(f.root) != nil {
		return ErrOperationUnavailable
	}
	return err
}

func (f *windowsOperationFiles) valid(ctx context.Context) error {
	if f == nil || f.closed || f.lease == nil || f.lease.Validate(ctx) != nil {
		return ErrOperationUnavailable
	}
	info, err := f.ops.info(f.root)
	if err != nil || !windowsDirectory(info) || windowsIdentity(info) != f.rootID || !windowsCurrentOwner(f.root) || !windowsLocalDisk(f.root) {
		return ErrOperationUnavailable
	}
	for _, allowed := range f.allowed {
		info, err := f.ops.info(allowed.handle)
		if err != nil || !windowsDirectory(info) || windowsIdentity(info) != allowed.id || !windowsCurrentOwner(allowed.handle) || !windowsLocalDisk(allowed.handle) {
			return ErrOperationUnavailable
		}
	}
	return nil
}

func (f *windowsOperationFiles) Read(ctx context.Context, logical string, offset, limit int64) (ReadResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.valid(ctx); err != nil {
		return ReadResult{}, err
	}
	if !windowsLogicalPath(logical) || offset < 0 || limit < 1 || limit > maxContent {
		return ReadResult{}, ErrOperationInvalidPath
	}
	h, err := f.openFile(logical, windows.FILE_GENERIC_READ|windows.READ_CONTROL)
	if err != nil {
		return ReadResult{}, err
	}
	defer f.ops.close(h)
	before, err := f.ops.info(h)
	if err != nil || before.FileSizeHigh != 0 || before.FileSizeLow > maxOperationFileBytes {
		return ReadResult{}, ErrOperationLimit
	}
	bytes, err := f.readExact(h, int(before.FileSizeLow))
	after, statErr := f.ops.info(h)
	if err != nil || statErr != nil || windowsIdentity(before) != windowsIdentity(after) || before.FileSizeLow != after.FileSizeLow || f.valid(ctx) != nil {
		return ReadResult{}, ErrOperationUnavailable
	}
	if offset > int64(len(bytes)) {
		offset = int64(len(bytes))
	}
	end := offset + limit
	if end < offset || end > int64(len(bytes)) {
		end = int64(len(bytes))
	}
	result, resultErr := NewReadResult(bytes, bytes[offset:end], offset, int64(len(bytes)), offset != 0 || end != int64(len(bytes)))
	if resultErr != nil {
		return ReadResult{}, ErrOperationUnavailable
	}
	return result, nil
}

func (f *windowsOperationFiles) Edit(ctx context.Context, logical, base string, replacements []Replacement) (result EditResult, resultErr error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.valid(ctx); err != nil {
		return EditResult{}, err
	}
	if !windowsLogicalPath(logical) || !f.editAllowed(logical) {
		return EditResult{}, ErrOperationInvalidPath
	}
	h, err := f.openFile(logical, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.READ_CONTROL)
	if err != nil {
		return EditResult{}, err
	}
	defer f.ops.close(h)
	release, lockErr := f.lockTarget(ctx, h)
	if lockErr != nil {
		return EditResult{}, lockErr
	}
	published := false
	defer func() {
		if err := release(); err != nil {
			if published {
				result, resultErr = EditResult{}, ErrOperationPublication
			} else if resultErr == nil {
				resultErr = ErrOperationUnavailable
			}
		}
	}()
	info, err := f.ops.info(h)
	if err != nil || info.FileSizeHigh != 0 || info.FileSizeLow > maxOperationFileBytes || !windowsCurrentOwner(h) {
		return EditResult{}, ErrOperationUnavailable
	}
	old, err := f.readExact(h, int(info.FileSizeLow))
	if err != nil || DigestSHA256(old) != base {
		return EditResult{}, ErrOperationConflict
	}
	proof, err := f.destinationProof(h, info, Digest(DigestSHA256(old)))
	if err != nil {
		return EditResult{}, ErrOperationUnavailable
	}
	final, err := windowsApplyReplacements(old, replacements)
	if err != nil || len(final) > maxOperationFileBytes {
		return EditResult{}, ErrOperationLimit
	}
	if string(final) == string(old) {
		return NewEditResult(old, false, "unchanged")
	}
	// The replacement is created below the same retained parent, so it cannot cross volumes.
	parent, name, err := f.parent(logical)
	if err != nil {
		return EditResult{}, ErrOperationUnavailable
	}
	defer f.ops.close(parent)
	tmp := ".direct-windows-candidate"
	candidate, err := f.create(parent, tmp)
	if err != nil {
		return EditResult{}, ErrOperationUnavailable
	}
	defer func() {
		if !published {
			_ = f.ops.cleanup(candidate)
		}
	}()
	if !f.applyCandidateProof(candidate, proof) || !f.candidateMatches(candidate, proof) {
		_ = f.ops.close(candidate)
		return EditResult{}, ErrOperationUnavailable
	}
	if f.writeExact(candidate, final) != nil || f.ops.flush(candidate) != nil {
		_ = f.ops.close(candidate)
		return EditResult{}, ErrOperationUnavailable
	}
	current, err := f.ops.info(h)
	if err != nil || windowsIdentity(current) != proof.id || current.FileSizeLow != info.FileSizeLow || !f.targetMatches(h, proof) {
		_ = f.ops.close(candidate)
		return EditResult{}, ErrOperationConflict
	}
	if err := f.ops.publish(candidate, parent, name, proof); err != nil {
		_ = f.ops.close(candidate)
		if errors.Is(err, errWindowsPublicationUnknown) {
			published = true
			return EditResult{}, ErrOperationPublication
		}
		return EditResult{}, ErrOperationUnavailable
	}
	published = true
	if !windowsSetSafeAttributes(candidate, proof.attrs) || f.ops.close(candidate) != nil || f.ops.flush(parent) != nil {
		return EditResult{}, ErrOperationPublication
	}
	verify, err := f.openFile(logical, windows.FILE_GENERIC_READ|windows.READ_CONTROL)
	if err != nil {
		return EditResult{}, ErrOperationPublication
	}
	verifyInfo, infoErr := f.ops.info(verify)
	got, readErr := f.readAll(verify)
	successor := proof
	successor.id = windowsIdentity(verifyInfo)
	successor.digest = Digest(DigestSHA256(final))
	verified := infoErr == nil && readErr == nil && string(got) == string(final) && f.targetMatches(verify, successor)
	closeErr := f.ops.close(verify)
	if closeErr != nil || !verified || f.valid(ctx) != nil {
		return EditResult{}, ErrOperationPublication
	}
	return NewEditResult(final, true, "published")
}

func (f *windowsOperationFiles) Tree(ctx context.Context, logical string) (InspectResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.valid(ctx); err != nil {
		return InspectResult{}, err
	}
	if logical != "" && !windowsLogicalPath(logical) {
		return InspectResult{}, ErrOperationInvalidPath
	}
	dir, err := f.walk(logical)
	if err != nil {
		return InspectResult{}, err
	}
	defer f.ops.close(dir)
	var lines []string
	if err := f.tree(ctx, dir, "/", 0, &lines); err != nil {
		return InspectResult{}, err
	}
	return NewInspectResult([]byte(strings.Join(lines, "\n")))
}

func (f *windowsOperationFiles) tree(ctx context.Context, dir windows.Handle, prefix string, depth int, lines *[]string) error {
	if depth > maxTreeDepth || len(*lines) > maxTreeEntries {
		return ErrOperationLimit
	}
	before, err := f.ops.info(dir)
	if err != nil || !windowsDirectory(before) {
		return ErrOperationUnavailable
	}
	entries, err := f.ops.enumerate(dir)
	if err != nil {
		return ErrOperationUnavailable
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		name := entry.Name()
		if !windowsName(name) {
			return ErrOperationUnavailable
		}
		child := strings.TrimSuffix(prefix, "/") + "/" + name
		if len(child) > windowsMaxTreePathBytes {
			return ErrOperationLimit
		}
		if entry.Type()&os.ModeSymlink != 0 {
			*lines = append(*lines, "l "+child)
			continue
		}
		h, openErr := f.ops.open(dir, name, entry.IsDir(), windows.FILE_GENERIC_READ|windows.READ_CONTROL)
		if openErr != nil {
			return ErrOperationConflict
		}
		info, infoErr := f.ops.info(h)
		if infoErr != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			_ = f.ops.close(h)
			return ErrOperationConflict
		}
		if windowsDirectory(info) {
			*lines = append(*lines, "d "+child)
			err = f.tree(ctx, h, child, depth+1, lines)
		} else if windowsRegular(info) {
			*lines = append(*lines, "f "+child)
		} else {
			err = ErrOperationUnavailable
		}
		closeErr := f.ops.close(h)
		if err != nil || closeErr != nil {
			if err != nil {
				return err
			}
			return ErrOperationUnavailable
		}
		if len(*lines) > maxTreeEntries {
			return ErrOperationLimit
		}
	}
	after, err := f.ops.info(dir)
	if err != nil || windowsIdentity(before) != windowsIdentity(after) {
		return ErrOperationConflict
	}
	return nil
}

func (f *windowsOperationFiles) walk(logical string) (windows.Handle, error) {
	var current windows.Handle
	err := windows.DuplicateHandle(windows.CurrentProcess(), f.root, windows.CurrentProcess(), &current, 0, false, windows.DUPLICATE_SAME_ACCESS)
	if err != nil {
		return 0, ErrOperationUnavailable
	}
	if logical == "" {
		return current, nil
	}
	for _, name := range strings.Split(logical, "/") {
		next, openErr := f.ops.open(current, name, true, windows.FILE_GENERIC_READ|windows.READ_CONTROL)
		_ = f.ops.close(current)
		if openErr != nil {
			return 0, ErrOperationUnavailable
		}
		current = next
	}
	return current, nil
}

func (f *windowsOperationFiles) parent(logical string) (windows.Handle, string, error) {
	parts := strings.Split(logical, "/")
	parent := strings.Join(parts[:len(parts)-1], "/")
	h, err := f.walk(parent)
	return h, parts[len(parts)-1], err
}
func (f *windowsOperationFiles) openFile(logical string, access uint32) (windows.Handle, error) {
	parent, name, err := f.parent(logical)
	if err != nil {
		return 0, err
	}
	defer f.ops.close(parent)
	h, err := f.ops.open(parent, name, false, access)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return 0, ErrOperationNotFound
	}
	if err != nil {
		return 0, ErrOperationUnavailable
	}
	info, statErr := f.ops.info(h)
	if statErr != nil || !windowsRegular(info) || !windowsCurrentOwner(h) {
		_ = f.ops.close(h)
		return 0, ErrOperationUnavailable
	}
	return h, nil
}
func (f *windowsOperationFiles) create(parent windows.Handle, name string) (windows.Handle, error) {
	return createWindowsRelative(parent, name)
}
func (f *windowsOperationFiles) editAllowed(logical string) bool {
	return f.allowedRoot(logical) != nil
}
func (f *windowsOperationFiles) allowedRoot(logical string) *windowsAllowedRoot {
	var selected *windowsAllowedRoot
	for i := range f.allowed {
		root := &f.allowed[i]
		if root.logical == "" || logical == root.logical || strings.HasPrefix(logical, root.logical+"/") {
			if selected == nil || len(root.logical) > len(selected.logical) {
				selected = root
			}
		}
	}
	return selected
}
func (f *windowsOperationFiles) readExact(h windows.Handle, size int) ([]byte, error) {
	value := make([]byte, size)
	for n := 0; n < len(value); {
		var got uint32
		if f.ops.read(h, value[n:], &got) != nil || got == 0 {
			return nil, ErrOperationUnavailable
		}
		n += int(got)
	}
	var extra [1]byte
	var got uint32
	if f.ops.read(h, extra[:], &got) != nil || got != 0 {
		return nil, ErrOperationUnavailable
	}
	return value, nil
}
func (f *windowsOperationFiles) readAll(h windows.Handle) ([]byte, error) {
	info, err := f.ops.info(h)
	if err != nil || info.FileSizeHigh != 0 || info.FileSizeLow > maxOperationFileBytes {
		return nil, ErrOperationUnavailable
	}
	return f.readExact(h, int(info.FileSizeLow))
}
func (f *windowsOperationFiles) writeExact(h windows.Handle, value []byte) error {
	for len(value) > 0 {
		var n uint32
		if f.ops.write(h, value, &n) != nil || n == 0 {
			return ErrOperationUnavailable
		}
		value = value[n:]
	}
	return nil
}

// lockTarget consumes completion for the exact overlapped request before it
// returns on cancellation, so a cancelled waiter cannot retain a lock.
func (f *windowsOperationFiles) lockTarget(ctx context.Context, handle windows.Handle) (func() error, error) {
	event, err := f.ops.createEvent()
	if err != nil {
		return nil, ErrOperationUnavailable
	}
	overlapped := &windows.Overlapped{HEvent: event}
	err = f.ops.lock(handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, overlapped)
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		cancelled := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				_ = f.ops.cancel(handle, overlapped)
			case <-cancelled:
			}
		}()
		status, waitErr := f.ops.wait(event, windows.INFINITE)
		close(cancelled)
		var bytes uint32
		completeErr := f.ops.result(handle, overlapped, &bytes, waitErr == nil && status == windows.WAIT_OBJECT_0)
		if waitErr != nil || status != windows.WAIT_OBJECT_0 || completeErr != nil {
			_ = f.ops.closeEvent(event)
			return nil, ErrOperationUnavailable
		}
	} else if err != nil {
		_ = f.ops.closeEvent(event)
		return nil, ErrOperationUnavailable
	}
	if ctx.Err() != nil {
		_ = f.ops.unlock(handle, 0, 1, 0, &windows.Overlapped{})
		_ = f.ops.closeEvent(event)
		return nil, ctx.Err()
	}
	used := false
	return func() error {
		if used {
			return ErrOperationUnavailable
		}
		used = true
		unlockErr := f.ops.unlock(handle, 0, 1, 0, &windows.Overlapped{})
		closeErr := f.ops.closeEvent(event)
		if unlockErr != nil || closeErr != nil {
			f.closed = true
			return ErrOperationUnavailable
		}
		return nil
	}, nil
}

func openWindowsRoot(path string) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(name, windows.FILE_GENERIC_READ|windows.READ_CONTROL|windows.SYNCHRONIZE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
}
func openWindowsRelative(parent windows.Handle, name string, directory bool, access uint32) (windows.Handle, error) {
	object, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, err
	}
	attributes := &windows.OBJECT_ATTRIBUTES{Length: uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})), RootDirectory: parent, ObjectName: object, Attributes: windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE}
	var h windows.Handle
	var status windows.IO_STATUS_BLOCK
	options := uint32(windows.FILE_SYNCHRONOUS_IO_NONALERT | windows.FILE_OPEN_REPARSE_POINT)
	attributesValue := uint32(windows.FILE_ATTRIBUTE_NORMAL)
	if directory {
		options |= windows.FILE_DIRECTORY_FILE
		attributesValue = windows.FILE_ATTRIBUTE_DIRECTORY
	} else {
		options |= windows.FILE_NON_DIRECTORY_FILE
	}
	err = windows.NtCreateFile(&h, access|windows.SYNCHRONIZE, attributes, &status, nil, attributesValue, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_OPEN, options, 0, 0)
	return h, err
}
func createWindowsRelative(parent windows.Handle, name string) (windows.Handle, error) {
	object, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, err
	}
	attributes := &windows.OBJECT_ATTRIBUTES{Length: uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})), RootDirectory: parent, ObjectName: object, Attributes: windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE}
	var h windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&h, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.DELETE|windows.READ_CONTROL|windows.SYNCHRONIZE, attributes, &status, nil, windows.FILE_ATTRIBUTE_NORMAL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_CREATE, windows.FILE_NON_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT, 0, 0)
	return h, err
}
func windowsDelete(h windows.Handle) error {
	value := byte(1)
	var status windows.IO_STATUS_BLOCK
	return windows.NtSetInformationFile(h, &status, &value, 1, windows.FileDispositionInformation)
}

var errWindowsPublicationUnknown = errors.New("Windows file publication outcome is unknown")

func windowsRenameRelative(source, parent windows.Handle, name string, expected windowsDestinationProof) error {
	// Reopen the destination under the retained parent immediately before rename.
	// Keeping this handle open binds the name check to the native replace syscall.
	destination, err := openWindowsRelative(parent, name, false, windows.FILE_GENERIC_READ|windows.READ_CONTROL)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(destination)
	if !windowsTargetMatches(destination, expected) {
		return windows.ERROR_ACCESS_DENIED
	}
	utf16, err := windows.UTF16FromString(name)
	if err != nil {
		return err
	}
	type rename struct {
		ReplaceIfExists uint32
		RootDirectory   windows.Handle
		FileNameLength  uint32
		FileName        [1]uint16
	}
	size := int(unsafe.Offsetof(rename{}.FileName)) + 2*(len(utf16)-1)
	data := make([]byte, size)
	value := (*rename)(unsafe.Pointer(&data[0]))
	value.ReplaceIfExists, value.RootDirectory, value.FileNameLength = 1, parent, uint32(2*(len(utf16)-1))
	copy((*[32767]uint16)(unsafe.Pointer(&value.FileName[0]))[:len(utf16)-1], utf16)
	var status windows.IO_STATUS_BLOCK
	if err := windows.NtSetInformationFile(source, &status, &data[0], uint32(len(data)), windows.FileRenameInformation); err != nil {
		return err
	}
	return nil
}
func windowsIdentity(info windows.ByHandleFileInformation) windowsFileID {
	return windowsFileID{info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow}
}
func windowsDirectory(info windows.ByHandleFileInformation) bool {
	return info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 && info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0
}
func windowsRegular(info windows.ByHandleFileInformation) bool {
	return info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) == 0
}
func windowsLocalDisk(h windows.Handle) bool {
	var flags uint32
	return windows.GetVolumeInformationByHandle(h, nil, 0, nil, nil, &flags, nil, 0) == nil && flags&0x10 == 0
}
func windowsCurrentOwner(h windows.Handle) bool {
	descriptor, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil || descriptor == nil {
		return false
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil {
		return false
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	return err == nil && user != nil && user.User.Sid != nil && owner.Equals(user.User.Sid)
}

const windowsSafeFileAttributes = windows.FILE_ATTRIBUTE_READONLY | windows.FILE_ATTRIBUTE_HIDDEN | windows.FILE_ATTRIBUTE_ARCHIVE | windows.FILE_ATTRIBUTE_NORMAL

type windowsBasicInformation struct {
	creationTime, lastAccessTime, lastWriteTime, changeTime int64
	attributes                                              uint32
}

type windowsACLHeader struct {
	revision, reserved byte
	size               uint16
}

func (f *windowsOperationFiles) destinationProof(h windows.Handle, info windows.ByHandleFileInformation, digest Digest) (windowsDestinationProof, error) {
	descriptor, err := f.ops.security(h, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return windowsDestinationProof{}, err
	}
	return windowsProofFromDescriptor(info, digest, descriptor)
}

func windowsProofFromDescriptor(info windows.ByHandleFileInformation, digest Digest, descriptor *windows.SECURITY_DESCRIPTOR) (windowsDestinationProof, error) {
	if !windowsRegular(info) || !windowsLocalAttributes(info.FileAttributes) || descriptor == nil || !descriptor.IsValid() {
		return windowsDestinationProof{}, windows.ERROR_ACCESS_DENIED
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil || !owner.IsValid() {
		return windowsDestinationProof{}, windows.ERROR_ACCESS_DENIED
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil || !owner.Equals(user.User.Sid) {
		return windowsDestinationProof{}, windows.ERROR_ACCESS_DENIED
	}
	control, _, err := descriptor.Control()
	if err != nil || control&windows.SE_DACL_PRESENT == 0 {
		return windowsDestinationProof{}, windows.ERROR_ACCESS_DENIED
	}
	dacl, defaulted, err := descriptor.DACL()
	aclSize := uint16(0)
	if dacl != nil {
		aclSize = (*windowsACLHeader)(unsafe.Pointer(dacl)).size
	}
	if err != nil || defaulted || dacl == nil || aclSize < uint16(unsafe.Sizeof(windows.ACL{})) {
		return windowsDestinationProof{}, windows.ERROR_ACCESS_DENIED
	}
	return windowsDestinationProof{
		id: windowsIdentity(info), digest: digest, owner: owner.String(),
		dacl:      append([]byte(nil), unsafe.Slice((*byte)(unsafe.Pointer(dacl)), aclSize)...),
		protected: control&windows.SE_DACL_PROTECTED != 0,
		attrs:     info.FileAttributes & windowsSafeFileAttributes,
	}, nil
}

func windowsLocalAttributes(attributes uint32) bool {
	if attributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DEVICE|windows.FILE_ATTRIBUTE_SPARSE_FILE|windows.FILE_ATTRIBUTE_COMPRESSED|windows.FILE_ATTRIBUTE_ENCRYPTED|windows.FILE_ATTRIBUTE_SYSTEM) != 0 || attributes&^uint32(windowsSafeFileAttributes) != 0 {
		return false
	}
	return attributes&windows.FILE_ATTRIBUTE_NORMAL == 0 || attributes == windows.FILE_ATTRIBUTE_NORMAL
}

func (f *windowsOperationFiles) applyCandidateProof(h windows.Handle, proof windowsDestinationProof) bool {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil || len(proof.dacl) == 0 {
		return false
	}
	dacl := (*windows.ACL)(unsafe.Pointer(&proof.dacl[0]))
	mask := windows.SECURITY_INFORMATION(windows.OWNER_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION)
	if proof.protected {
		mask |= windows.PROTECTED_DACL_SECURITY_INFORMATION
	} else {
		mask |= windows.UNPROTECTED_DACL_SECURITY_INFORMATION
	}
	// A read-only target is intentionally writable only while this private temp is written.
	return f.ops.setSecurity(h, mask, user.User.Sid, dacl) == nil && windowsSetSafeAttributes(h, proof.attrs&^windows.FILE_ATTRIBUTE_READONLY)
}

func (f *windowsOperationFiles) candidateMatches(h windows.Handle, proof windowsDestinationProof) bool {
	info, err := f.ops.info(h)
	if err != nil || info.FileSizeHigh != 0 || info.FileSizeLow != 0 || info.FileAttributes != proof.attrs&^windows.FILE_ATTRIBUTE_READONLY {
		return false
	}
	got, err := f.destinationProof(h, info, "")
	return err == nil && got.owner == proof.owner && got.protected == proof.protected && string(got.dacl) == string(proof.dacl)
}

func (f *windowsOperationFiles) targetMatches(h windows.Handle, proof windowsDestinationProof) bool {
	info, err := f.ops.info(h)
	if err != nil || windowsIdentity(info) != proof.id || info.FileAttributes&windowsSafeFileAttributes != proof.attrs || !windowsLocalAttributes(info.FileAttributes) {
		return false
	}
	got, err := f.destinationProof(h, info, proof.digest)
	if err != nil || got.owner != proof.owner || got.protected != proof.protected || string(got.dacl) != string(proof.dacl) {
		return false
	}
	if proof.digest == "" {
		return true
	}
	bytes, err := windowsReadHandle(h, info)
	return err == nil && Digest(DigestSHA256(bytes)) == proof.digest
}

func windowsTargetMatches(h windows.Handle, expected windowsDestinationProof) bool {
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &info) != nil || windowsIdentity(info) != expected.id || !windowsLocalAttributes(info.FileAttributes) || info.FileAttributes&windowsSafeFileAttributes != expected.attrs {
		return false
	}
	descriptor, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false
	}
	got, err := windowsProofFromDescriptor(info, expected.digest, descriptor)
	if err != nil || got.owner != expected.owner || got.protected != expected.protected || string(got.dacl) != string(expected.dacl) {
		return false
	}
	bytes, err := windowsReadHandle(h, info)
	return err == nil && Digest(DigestSHA256(bytes)) == expected.digest
}

func windowsReadHandle(h windows.Handle, info windows.ByHandleFileInformation) ([]byte, error) {
	if info.FileSizeHigh != 0 || info.FileSizeLow > maxOperationFileBytes {
		return nil, windows.ERROR_FILE_TOO_LARGE
	}
	if _, err := windows.SetFilePointer(h, 0, nil, windows.FILE_BEGIN); err != nil {
		return nil, err
	}
	bytes := make([]byte, int(info.FileSizeLow))
	for offset := 0; offset < len(bytes); {
		var n uint32
		if err := windows.ReadFile(h, bytes[offset:], &n, nil); err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, errors.New("short Windows file read")
		}
		offset += int(n)
	}
	return bytes, nil
}

func windowsSetSafeAttributes(h windows.Handle, attributes uint32) bool {
	if !windowsLocalAttributes(attributes) {
		return false
	}
	if attributes == 0 {
		attributes = windows.FILE_ATTRIBUTE_NORMAL
	}
	info := windowsBasicInformation{creationTime: -1, lastAccessTime: -1, lastWriteTime: -1, changeTime: -1, attributes: attributes}
	return windows.SetFileInformationByHandle(h, windows.FileBasicInformation, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))) == nil
}
func windowsLogicalPath(value string) bool {
	if path([]byte(`"`+value+`"`)) != nil || strings.ContainsAny(value, "\\:") || value != strings.TrimSpace(value) {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if !windowsName(component) {
			return false
		}
	}
	return true
}
func windowsName(value string) bool {
	return value != "" && len(value) <= windowsMaxTreeNameBytes && strings.TrimSpace(value) == value && !strings.HasSuffix(value, ".") && !reservedDevice(value) && !strings.ContainsAny(value, "\\/:\x00\r\n")
}
func logicalEditRootWindows(repository, configured string) (string, bool) {
	relative, err := filepath.Rel(repository, configured)
	if err != nil || relative == "." {
		return "", err == nil
	}
	relative = filepath.ToSlash(relative)
	return relative, windowsLogicalPath(relative)
}

const (
	windowsMaxTreeNameBytes = 255
	windowsMaxTreePathBytes = 1024
)

func windowsApplyReplacements(old []byte, values []Replacement) ([]byte, error) {
	var out []byte
	current := int64(0)
	for _, value := range values {
		if value.Start < current || value.End < value.Start || value.End > int64(len(old)) {
			return nil, ErrOperationConflict
		}
		out = append(out, old[current:value.Start]...)
		out = append(out, value.Text...)
		current = value.End
	}
	return append(out, old[current:]...), nil
}
