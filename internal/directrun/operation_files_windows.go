//go:build windows

package directrun

import (
	"context"
	"errors"
	"io"
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
	allowed map[string]windowsFileID
	closed  bool
	ops     windowsOperationFileOps
}

type windowsFileID struct{ volume, high, low uint32 }

type windowsOperationFileOps struct {
	open  func(windows.Handle, string, bool, uint32) (windows.Handle, error)
	info  func(windows.Handle) (windows.ByHandleFileInformation, error)
	read  func(windows.Handle, []byte, *uint32) error
	write func(windows.Handle, []byte, *uint32) error
	flush func(windows.Handle) error
	close func(windows.Handle) error
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
	f := &windowsOperationFiles{lease: lease, root: root, allowed: make(map[string]windowsFileID), ops: newWindowsOperationFileOps()}
	info, err := f.ops.info(root)
	if err != nil || !windowsDirectory(info) || !windowsCurrentOwner(root) || !windowsLocalDisk(root) {
		_ = f.ops.close(root)
		return nil, ErrOperationUnavailable
	}
	f.rootID = windowsIdentity(info)
	for _, configured := range handoff.AllowedEditRoots {
		logical, ok := logicalEditRootWindows(lease.Identity().RepositoryRoot, configured)
		if !ok {
			_ = f.ops.close(root)
			return nil, ErrOperationUnavailable
		}
		dir, err := f.walk(logical)
		if err != nil {
			_ = f.ops.close(root)
			return nil, ErrOperationUnavailable
		}
		info, statErr := f.ops.info(dir)
		_ = f.ops.close(dir)
		if statErr != nil || !windowsDirectory(info) {
			_ = f.ops.close(root)
			return nil, ErrOperationUnavailable
		}
		f.allowed[logical] = windowsIdentity(info)
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
	if f.ops.close(f.root) != nil {
		return ErrOperationUnavailable
	}
	return nil
}

func (f *windowsOperationFiles) valid(ctx context.Context) error {
	if f == nil || f.closed || f.lease == nil || f.lease.Validate(ctx) != nil {
		return ErrOperationUnavailable
	}
	info, err := f.ops.info(f.root)
	if err != nil || !windowsDirectory(info) || windowsIdentity(info) != f.rootID || !windowsCurrentOwner(f.root) || !windowsLocalDisk(f.root) {
		return ErrOperationUnavailable
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
	h, err := f.openFile(logical)
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

func (f *windowsOperationFiles) Edit(ctx context.Context, logical, base string, replacements []Replacement) (EditResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.valid(ctx); err != nil {
		return EditResult{}, err
	}
	if !windowsLogicalPath(logical) || !f.editAllowed(logical) {
		return EditResult{}, ErrOperationInvalidPath
	}
	h, err := f.openFile(logical)
	if err != nil {
		return EditResult{}, err
	}
	defer f.ops.close(h)
	info, err := f.ops.info(h)
	if err != nil || info.FileSizeHigh != 0 || info.FileSizeLow > maxOperationFileBytes || !windowsCurrentOwner(h) {
		return EditResult{}, ErrOperationUnavailable
	}
	old, err := f.readExact(h, int(info.FileSizeLow))
	if err != nil || DigestSHA256(old) != base {
		return EditResult{}, ErrOperationConflict
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
	published := false
	defer func() {
		if !published {
			_ = windowsDelete(candidate)
		}
	}()
	if f.writeExact(candidate, final) != nil || f.ops.flush(candidate) != nil {
		_ = f.ops.close(candidate)
		return EditResult{}, ErrOperationUnavailable
	}
	current, err := f.ops.info(h)
	if err != nil || windowsIdentity(current) != windowsIdentity(info) || current.FileSizeLow != info.FileSizeLow {
		_ = f.ops.close(candidate)
		return EditResult{}, ErrOperationConflict
	}
	if err := windowsRenameRelative(candidate, parent, name); err != nil {
		_ = f.ops.close(candidate)
		return EditResult{}, ErrOperationUnavailable
	}
	published = true
	if f.ops.flush(parent) != nil {
		return EditResult{}, ErrOperationPublication
	}
	verify, err := f.openFile(logical)
	if err != nil {
		return EditResult{}, ErrOperationPublication
	}
	got, readErr := f.readAll(verify)
	closeErr := f.ops.close(verify)
	if readErr != nil || closeErr != nil || string(got) != string(final) || f.valid(ctx) != nil {
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
	var duplicate windows.Handle
	if windows.DuplicateHandle(windows.CurrentProcess(), dir, windows.CurrentProcess(), &duplicate, 0, false, windows.DUPLICATE_SAME_ACCESS) != nil {
		return ErrOperationUnavailable
	}
	file := os.NewFile(uintptr(duplicate), "")
	entries, err := file.ReadDir(-1)
	_ = file.Close()
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
func (f *windowsOperationFiles) openFile(logical string) (windows.Handle, error) {
	parent, name, err := f.parent(logical)
	if err != nil {
		return 0, err
	}
	defer f.ops.close(parent)
	h, err := f.ops.open(parent, name, false, windows.FILE_GENERIC_READ|windows.READ_CONTROL)
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
	for root, want := range f.allowed {
		if root == "" || logical == root || strings.HasPrefix(logical, root+"/") {
			h, err := f.walk(root)
			if err != nil {
				return false
			}
			info, statErr := f.ops.info(h)
			_ = f.ops.close(h)
			return statErr == nil && windowsIdentity(info) == want
		}
	}
	return false
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
func windowsRenameRelative(source, parent windows.Handle, name string) error {
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
	return windows.NtSetInformationFile(source, &status, &data[0], uint32(len(data)), windows.FileRenameInformation)
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

var _ = io.EOF

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
