//go:build windows

package filecoord

import (
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"
)

type windowsLockNtCreateFile func(*windows.Handle, uint32, *windows.OBJECT_ATTRIBUTES, *windows.IO_STATUS_BLOCK, *int64, uint32, uint32, uint32, uint32, uintptr, uint32) error

var (
	nativeWindowsLockCreateFile windowsLockNtCreateFile = windows.NtCreateFile
	currentWindowsLockSID                               = currentWindowsLockSIDDefault
	windowsLockWrapFile                                 = func(h windows.Handle, name string) *os.File { return os.NewFile(uintptr(h), name) }
	windowsLockGetFileInfo                              = windows.GetFileInformationByHandle
	windowsLockGetFileType                              = windows.GetFileType
	windowsLockGetSecurityInfo                          = func(h windows.Handle) (*windows.SECURITY_DESCRIPTOR, error) {
		return windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	}
	closeAcquisitionFile      = func(file *os.File) error { return file.Close() }
	errUnsafeWindowsLockState = errors.New("unsafe Windows lock object state")
)

const (
	windowsLockRootAccess windows.ACCESS_MASK = windows.ACCESS_MASK(windows.FILE_WRITE_DATA | windows.FILE_READ_ATTRIBUTES | windows.READ_CONTROL | windows.SYNCHRONIZE)
	windowsLockFileAccess windows.ACCESS_MASK = windows.ACCESS_MASK(windows.FILE_READ_DATA | windows.FILE_WRITE_DATA | windows.FILE_READ_ATTRIBUTES | windows.READ_CONTROL | windows.SYNCHRONIZE)
)

func openPrivateRoot(path string) (*os.File, error) {
	path = filepath.Clean(path)
	volume, sep := filepath.VolumeName(path), string(filepath.Separator)
	if volume == "" || !strings.HasPrefix(path, volume+sep) || strings.HasPrefix(path, `\\`) || path == volume+sep {
		return nil, &InvalidRootError{}
	}
	sid, err := currentWindowsLockSID()
	if err != nil {
		return nil, &OperationalError{Cause: err}
	}
	descriptor, err := windowsLockSecurityDescriptor(sid, true)
	if err != nil {
		return nil, &OperationalError{Cause: err}
	}
	handle, err := createWindowsLockObject(0, `\??\`+path, true, descriptor, nativeWindowsLockCreateFile)
	if err != nil {
		return nil, rootError(err)
	}
	file := windowsLockWrapFile(handle, path)
	if err := validateWindowsLockObject(handle, sid, true, false); err != nil {
		return nil, closeAcquisitionFailure(rootError(err), file)
	}
	return file, nil
}
func openLockFile(root *os.File, name string, sid *windows.SID) (*os.File, error) {
	if root == nil || name == "" || filepath.Base(name) != name || strings.ContainsAny(name, `/\`) {
		return nil, &OperationalError{Cause: errUnsafeWindowsLockState}
	}
	descriptor, err := windowsLockSecurityDescriptor(sid, false)
	if err != nil {
		return nil, &OperationalError{Cause: err}
	}
	handle, err := createWindowsLockObject(windows.Handle(root.Fd()), name, false, descriptor, nativeWindowsLockCreateFile)
	if err != nil {
		return nil, &OperationalError{Cause: err}
	}
	file := windowsLockWrapFile(handle, name)
	if err := validateWindowsLockObject(handle, sid, false, true); err != nil {
		return nil, closeAcquisitionFailure(&OperationalError{Cause: err}, file)
	}
	return file, nil
}
func createWindowsLockObject(parent windows.Handle, name string, directory bool, descriptor *windows.SECURITY_DESCRIPTOR, create windowsLockNtCreateFile) (windows.Handle, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, err
	}
	attributes := &windows.OBJECT_ATTRIBUTES{Length: uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})), RootDirectory: parent, ObjectName: objectName, Attributes: windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE, SecurityDescriptor: descriptor}
	options := uint32(windows.FILE_SYNCHRONOUS_IO_NONALERT | windows.FILE_OPEN_REPARSE_POINT)
	if directory {
		options |= windows.FILE_DIRECTORY_FILE
	} else {
		options |= windows.FILE_NON_DIRECTORY_FILE
	}
	access, _ := windowsLockRule(directory)
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = create(&handle, uint32(access), attributes, &status, nil, windows.FILE_ATTRIBUTE_NORMAL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_OPEN_IF, options, 0, 0)
	runtime.KeepAlive(descriptor)
	return handle, err
}
func validateWindowsLockObject(handle windows.Handle, sid *windows.SID, directory, oneLink bool) error {
	var info windows.ByHandleFileInformation
	if err := windowsLockGetFileInfo(handle, &info); err != nil {
		return err
	}
	fileType, err := windowsLockGetFileType(handle)
	if err != nil {
		return err
	}
	if fileType != windows.FILE_TYPE_DISK || (info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != directory || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || oneLink && info.NumberOfLinks != 1 {
		return errUnsafeWindowsLockState
	}
	return validateWindowsLockSecurity(handle, sid, directory)
}
func validateWindowsLockSecurity(handle windows.Handle, sid *windows.SID, directory bool) error {
	if sid == nil || !sid.IsValid() {
		return errUnsafeWindowsLockState
	}
	descriptor, err := windowsLockGetSecurityInfo(handle)
	if err != nil {
		return err
	}
	if descriptor == nil || !descriptor.IsValid() {
		return errUnsafeWindowsLockState
	}
	control, _, err := descriptor.Control()
	if err != nil {
		return err
	}
	if control&windows.SE_DACL_PRESENT == 0 || control&windows.SE_DACL_PROTECTED == 0 || control&windows.SE_DACL_DEFAULTED != 0 {
		return errUnsafeWindowsLockState
	}
	owner, ownerDefaulted, err := descriptor.Owner()
	if err != nil {
		return err
	}
	if ownerDefaulted || owner == nil || !owner.IsValid() || !owner.Equals(sid) {
		return errUnsafeWindowsLockState
	}
	dacl, daclDefaulted, err := descriptor.DACL()
	if err != nil {
		return err
	}
	if daclDefaulted || dacl == nil || dacl.AceCount != 1 {
		return errUnsafeWindowsLockState
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	access, flags := windowsLockRule(directory)
	if err := windows.GetAce(dacl, 0, &ace); err != nil || ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || windows.ACCESS_MASK(ace.Mask) != access || ace.Header.AceFlags != flags {
		return errUnsafeWindowsLockState
	}
	const sidOffset = unsafe.Offsetof(windows.ACCESS_ALLOWED_ACE{}.SidStart)
	if uintptr(ace.Header.AceSize) < sidOffset+unsafe.Sizeof(windows.SID{}) {
		return errUnsafeWindowsLockState
	}
	aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if !aceSID.IsValid() || uintptr(ace.Header.AceSize) != sidOffset+uintptr(aceSID.Len()) || !aceSID.Equals(sid) {
		return errUnsafeWindowsLockState
	}
	return nil
}
func windowsLockRule(directory bool) (windows.ACCESS_MASK, uint8) {
	if directory {
		return windowsLockRootAccess, windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE
	}
	return windowsLockFileAccess, 0
}
func windowsLockSecurityDescriptor(sid *windows.SID, directory bool) (*windows.SECURITY_DESCRIPTOR, error) {
	if sid == nil || !sid.IsValid() || sid.String() == "" {
		return nil, errUnsafeWindowsLockState
	}
	inheritance := map[bool]string{false: "", true: "OICI"}[directory]
	access, _ := windowsLockRule(directory)
	sidText := sid.String()
	return windows.SecurityDescriptorFromString(fmt.Sprintf("O:%sD:P(A;%s;0x%08x;;;%s)", sidText, inheritance, access, sidText))
}
func currentWindowsLockSIDDefault() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil || !user.User.Sid.IsValid() {
		return nil, err
	}
	return user.User.Sid.Copy()
}
func rootError(err error) error {
	if errors.Is(err, errUnsafeWindowsLockState) || errors.Is(err, windows.STATUS_REPARSE_POINT_ENCOUNTERED) || errors.Is(err, windows.STATUS_NOT_A_DIRECTORY) || errors.Is(err, windows.STATUS_FILE_IS_A_DIRECTORY) || errors.Is(err, windows.STATUS_OBJECT_PATH_NOT_FOUND) || errors.Is(err, windows.STATUS_OBJECT_NAME_NOT_FOUND) {
		return errors.Join(&InvalidRootError{}, err)
	}
	return &OperationalError{Cause: err}
}
func closeAcquisitionFailure(primary error, files ...*os.File) error {
	var cleanup error
	for _, file := range files {
		if file != nil {
			cleanup = errors.Join(cleanup, closeAcquisitionFile(file))
		}
	}
	if cleanup == nil {
		return primary
	}
	return errors.Join(primary, &OperationalError{Cause: cleanup})
}
