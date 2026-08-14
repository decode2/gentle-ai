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

var (
	errSecureWindowsChildInvalid = errors.New("secure Windows child access is invalid") // refusal:by-design world-action: the caller must provide a valid parent authority and child name before access can proceed
	errSecureWindowsChildMissing = errors.New("secure Windows child is missing")        // refusal:by-design world-action: the caller selects whether to create the child or open a different existing child
	errSecureWindowsChildExists  = errors.New("secure Windows child already exists")    // refusal:by-design world-action: the caller selects conflict handling before retrying creation
)

type secureWindowsChildID struct{ volume, high, low uint32 }

// secureWindowsChild owns a directory handle opened relative to a retained
// parent. Its handle and identity deliberately stay package-private.
type secureWindowsChild struct {
	mu     sync.Mutex
	handle windows.Handle
	id     secureWindowsChildID
}

// secureWindowsData owns a regular private data-file handle. Like directory
// children, its native handle and identity never leave this package.
type secureWindowsData struct {
	handle windows.Handle
	id     secureWindowsChildID
}

func openSecureWindowsChildDirectory(ctx context.Context, parent windows.Handle, want *secureWindowsChildID, name string) (*secureWindowsChild, error) {
	return secureWindowsChildDirectory(ctx, parent, want, name, windows.FILE_OPEN)
}

func createSecureWindowsChildDirectory(ctx context.Context, parent windows.Handle, want *secureWindowsChildID, name string) (*secureWindowsChild, error) {
	return secureWindowsChildDirectory(ctx, parent, want, name, windows.FILE_CREATE)
}

func openSecureWindowsChildData(ctx context.Context, parent windows.Handle, want *secureWindowsChildID, name string) (*secureWindowsData, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !secureWindowsChildName(name) || !secureWindowsDirectoryHandle(parent, want) {
		return nil, errSecureWindowsChildInvalid
	}
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, errSecureWindowsChildInvalid
	}
	attributes := &windows.OBJECT_ATTRIBUTES{Length: uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})), RootDirectory: parent, ObjectName: objectName, Attributes: windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE}
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&handle, windows.FILE_GENERIC_READ|windows.READ_CONTROL|windows.SYNCHRONIZE, attributes, &status, nil,
		windows.FILE_ATTRIBUTE_NORMAL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT, 0, 0)
	if err != nil {
		return nil, secureWindowsChildError(err)
	}
	data := &secureWindowsData{handle: handle}
	if !secureWindowsDirectoryHandle(parent, want) || !data.valid() {
		_ = data.Close()
		return nil, errSecureWindowsChildInvalid
	}
	return data, nil
}

// createSecureWindowsChildData creates one empty, owner-only regular file under
// the retained parent. The caller supplies only an internal unpredictable name.
func createSecureWindowsChildData(ctx context.Context, parent windows.Handle, want *secureWindowsChildID, name string) (*secureWindowsData, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !secureWindowsChildName(name) || !secureWindowsDirectoryHandle(parent, want) {
		return nil, errSecureWindowsChildInvalid
	}
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, errSecureWindowsChildInvalid
	}
	descriptor, err := ownerOnlyRARSecurityDescriptor(false)
	if err != nil {
		return nil, errSecureWindowsChildInvalid
	}
	defer runtime.KeepAlive(descriptor)
	attributes := &windows.OBJECT_ATTRIBUTES{Length: uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})), RootDirectory: parent, ObjectName: objectName, Attributes: windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE, SecurityDescriptor: descriptor}
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&handle, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.DELETE|windows.READ_CONTROL|windows.SYNCHRONIZE, attributes, &status, nil,
		windows.FILE_ATTRIBUTE_NORMAL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_CREATE, windows.FILE_NON_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT, 0, 0)
	if err != nil {
		return nil, secureWindowsChildError(err)
	}
	data := &secureWindowsData{handle: handle}
	info, _, ok := secureWindowsDataInfo(handle)
	if !secureWindowsDirectoryHandle(parent, want) || !ok || !data.valid() || info.FileSizeHigh != 0 || info.FileSizeLow != 0 {
		_ = secureWindowsDeleteData(data)
		return nil, errSecureWindowsChildInvalid
	}
	return data, nil
}

func secureWindowsChildDirectory(ctx context.Context, parent windows.Handle, want *secureWindowsChildID, name string, disposition uint32) (*secureWindowsChild, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !secureWindowsChildName(name) || !secureWindowsDirectoryHandle(parent, want) {
		return nil, errSecureWindowsChildInvalid
	}
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, errSecureWindowsChildInvalid
	}
	attributes := &windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: parent,
		ObjectName:    objectName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
	}
	if disposition == windows.FILE_CREATE {
		descriptor, descriptorErr := ownerOnlyRARSecurityDescriptor(true)
		if descriptorErr != nil {
			return nil, errSecureWindowsChildInvalid
		}
		attributes.SecurityDescriptor = descriptor
		defer runtime.KeepAlive(descriptor)
	}
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	access := uint32(windows.FILE_GENERIC_READ | windows.READ_CONTROL)
	if disposition == windows.FILE_CREATE {
		access |= windows.FILE_GENERIC_WRITE
	}
	err = windows.NtCreateFile(&handle, access, attributes, &status, nil,
		windows.FILE_ATTRIBUTE_DIRECTORY, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		disposition, windows.FILE_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT, 0, 0)
	if err != nil {
		return nil, secureWindowsChildError(err)
	}
	child := &secureWindowsChild{handle: handle}
	if !secureWindowsDirectoryHandle(parent, want) || !child.validLocked() {
		_ = child.Close()
		return nil, errSecureWindowsChildInvalid
	}
	return child, nil
}

func (child *secureWindowsChild) Close() error {
	if child == nil {
		return nil
	}
	child.mu.Lock()
	defer child.mu.Unlock()
	if child.handle == 0 {
		return nil
	}
	err := windows.CloseHandle(child.handle)
	child.handle = 0
	return err
}

func (child *secureWindowsChild) validLocked() bool {
	info, id, ok := secureWindowsDirectoryInfo(child.handle)
	if !ok || !privateSecureWindowsDirectory(child.handle) {
		return false
	}
	if child.id == (secureWindowsChildID{}) {
		child.id = id
	}
	return child.id == id && info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0
}

func (child *secureWindowsChild) valid() bool {
	if child == nil {
		return false
	}
	child.mu.Lock()
	defer child.mu.Unlock()
	return child.validLocked()
}

func (data *secureWindowsData) Close() error {
	if data == nil || data.handle == 0 {
		return nil
	}
	err := windows.CloseHandle(data.handle)
	data.handle = 0
	return err
}

func (data *secureWindowsData) valid() bool {
	info, id, ok := secureWindowsDataInfo(data.handle)
	if !ok || !privateSecureWindowsData(data.handle) {
		return false
	}
	if data.id == (secureWindowsChildID{}) {
		data.id = id
	}
	return data.id == id && info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0
}

func secureWindowsDeleteData(data *secureWindowsData) error {
	if data == nil || data.handle == 0 {
		return nil
	}
	deleteFile := byte(1)
	var status windows.IO_STATUS_BLOCK
	err := windows.NtSetInformationFile(data.handle, &status, &deleteFile, 1, windows.FileDispositionInformation)
	if closeErr := data.Close(); err == nil {
		err = closeErr
	}
	return err
}

func secureWindowsDirectoryHandle(handle windows.Handle, want *secureWindowsChildID) bool {
	_, id, ok := secureWindowsDirectoryInfo(handle)
	return ok && (want == nil || *want == id)
}

func secureWindowsDirectoryInfo(handle windows.Handle) (windows.ByHandleFileInformation, secureWindowsChildID, bool) {
	var info windows.ByHandleFileInformation
	if handle == 0 || windows.GetFileInformationByHandle(handle, &info) != nil {
		return info, secureWindowsChildID{}, false
	}
	fileType, err := windows.GetFileType(handle)
	if err != nil || fileType != windows.FILE_TYPE_DISK || info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != windows.FILE_ATTRIBUTE_DIRECTORY {
		return info, secureWindowsChildID{}, false
	}
	return info, secureWindowsChildID{info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow}, true
}

func secureWindowsDataInfo(handle windows.Handle) (windows.ByHandleFileInformation, secureWindowsChildID, bool) {
	var info windows.ByHandleFileInformation
	if handle == 0 || windows.GetFileInformationByHandle(handle, &info) != nil {
		return info, secureWindowsChildID{}, false
	}
	fileType, err := windows.GetFileType(handle)
	if err != nil || fileType != windows.FILE_TYPE_DISK || info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 {
		return info, secureWindowsChildID{}, false
	}
	return info, secureWindowsChildID{info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow}, true
}

func privateSecureWindowsDirectory(handle windows.Handle) bool {
	descriptor, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	return err == nil && privateRARSecurityDescriptorSafe(descriptor, true)
}

func privateSecureWindowsData(handle windows.Handle) bool {
	descriptor, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	return err == nil && privateRARSecurityDescriptorSafe(descriptor, false)
}

func secureWindowsChildError(err error) error {
	if errors.Is(err, windows.STATUS_OBJECT_NAME_NOT_FOUND) || errors.Is(err, windows.STATUS_NO_SUCH_FILE) {
		return errSecureWindowsChildMissing
	}
	if errors.Is(err, windows.STATUS_OBJECT_NAME_COLLISION) {
		return errSecureWindowsChildExists
	}
	return errSecureWindowsChildInvalid
}
