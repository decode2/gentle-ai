//go:build windows

package filecoord

import (
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"testing"
	"unsafe"
)

func windowsLockTestFatal(t *testing.T, err error) {
	if err != nil {
		t.Fatal(err)
	}
}
func windowsLockTestAssert(t *testing.T, ok bool, format string, args ...any) {
	if !ok {
		t.Fatalf(format, args...)
	}
}
func windowsLockTestDescriptor(t *testing.T, sid *windows.SID, directory bool) *windows.SECURITY_DESCRIPTOR {
	access, inheritance := windowsLockFileAccess, ""
	if directory {
		access, inheritance = windowsLockRootAccess, "OICI"
	}
	descriptor, err := windows.SecurityDescriptorFromString(fmt.Sprintf("O:%sD:P(A;%s;0x%08x;;;%s)", sid.String(), inheritance, access, sid.String()))
	windowsLockTestFatal(t, err)
	return descriptor
}
func TestWindowsLockNtCreateFileContract(t *testing.T) {
	type call struct {
		path                                                  string
		root                                                  windows.Handle
		access, attrs, fileAttrs, share, disposition, options uint32
	}
	var calls []call
	sid, err := currentWindowsLockSID()
	windowsLockTestFatal(t, err)
	rootDescriptor, fileDescriptor := windowsLockTestDescriptor(t, sid, true), windowsLockTestDescriptor(t, sid, false)
	wantDescriptors := [...]string{rootDescriptor.String(), fileDescriptor.String()}
	create := func(handle *windows.Handle, access uint32, attrs *windows.OBJECT_ATTRIBUTES, status *windows.IO_STATUS_BLOCK, allocation *int64, fileAttrs, share, disposition, options uint32, ea uintptr, eaLength uint32) error {
		if allocation != nil || ea != 0 || eaLength != 0 || status == nil || attrs.Length != uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})) || attrs.SecurityQoS != nil || attrs.SecurityDescriptor == nil || len(calls) >= len(wantDescriptors) || attrs.SecurityDescriptor.String() != wantDescriptors[len(calls)] {
			t.Errorf("unexpected NtCreateFile contract arguments")
		}
		calls = append(calls, call{attrs.ObjectName.String(), attrs.RootDirectory, access, attrs.Attributes, fileAttrs, share, disposition, options})
		*handle = windows.Handle(40 + len(calls))
		return nil
	}
	root, err := createWindowsLockObject(0, `\??\C:\existing\root`, true, rootDescriptor, create)
	windowsLockTestFatal(t, err)
	file, err := createWindowsLockObject(root, "hash.lock", false, fileDescriptor, create)
	windowsLockTestFatal(t, err)
	windowsLockTestAssert(t, len(calls) == 2 && root == 41 && file == 42, "calls=%d handles=%d,%d", len(calls), root, file)
	wantAccess := []uint32{uint32(windowsLockRootAccess), uint32(windowsLockFileAccess)}
	wantOptions := []uint32{windows.FILE_SYNCHRONOUS_IO_NONALERT | windows.FILE_OPEN_REPARSE_POINT | windows.FILE_DIRECTORY_FILE, windows.FILE_SYNCHRONOUS_IO_NONALERT | windows.FILE_OPEN_REPARSE_POINT | windows.FILE_NON_DIRECTORY_FILE}
	wantPaths := []string{`\??\C:\existing\root`, "hash.lock"}
	for i, got := range calls {
		windowsLockTestAssert(t, got.path == wantPaths[i] && got.access == wantAccess[i] && got.attrs == windows.OBJ_CASE_INSENSITIVE|windows.OBJ_DONT_REPARSE && got.fileAttrs == windows.FILE_ATTRIBUTE_NORMAL && got.share == windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE && got.disposition == windows.FILE_OPEN_IF && got.options == wantOptions[i], "NtCreateFile call %d = %#v", i, got)
	}
	windowsLockTestAssert(t, calls[0].root == 0 && calls[1].root == root, "RootDirectory relation = %v,%v", calls[0].root, calls[1].root)
}
func TestWindowsLockOpenLockFileOwnershipAndClose(t *testing.T) {
	oldCreate, oldWrap, oldClose := nativeWindowsLockCreateFile, windowsLockWrapFile, closeAcquisitionFile
	oldInfo, oldType, oldSecurity := windowsLockGetFileInfo, windowsLockGetFileType, windowsLockGetSecurityInfo
	t.Cleanup(func() {
		nativeWindowsLockCreateFile, windowsLockWrapFile, closeAcquisitionFile = oldCreate, oldWrap, oldClose
		windowsLockGetFileInfo, windowsLockGetFileType, windowsLockGetSecurityInfo = oldInfo, oldType, oldSecurity
	})
	sid, err := currentWindowsLockSID()
	windowsLockTestFatal(t, err)
	descriptor := windowsLockTestDescriptor(t, sid, false)
	links, wraps, closes := uint32(1), 0, 0
	closeErr := errors.New("injected close failure")
	nativeWindowsLockCreateFile = func(handle *windows.Handle, _ uint32, _ *windows.OBJECT_ATTRIBUTES, _ *windows.IO_STATUS_BLOCK, _ *int64, _, _, _, _ uint32, _ uintptr, _ uint32) error {
		*handle = 7
		return nil
	}
	windowsLockWrapFile = func(handle windows.Handle, name string) *os.File { wraps++; return os.NewFile(uintptr(handle), name) }
	closeAcquisitionFile = func(*os.File) error { closes++; return closeErr }
	windowsLockGetFileInfo = func(_ windows.Handle, info *windows.ByHandleFileInformation) error {
		info.NumberOfLinks = links
		return nil
	}
	windowsLockGetFileType = func(windows.Handle) (uint32, error) { return windows.FILE_TYPE_DISK, nil }
	windowsLockGetSecurityInfo = func(windows.Handle) (*windows.SECURITY_DESCRIPTOR, error) { return descriptor, nil }
	file, err := openLockFile(os.NewFile(9, "root"), "hash.lock", sid)
	windowsLockTestAssert(t, err == nil && file != nil && file.Fd() == 7 && wraps == 1, "success: file=%v err=%v wraps=%d", file, err, wraps)
	dacl, _, _ := descriptor.DACL()
	var ace *windows.ACCESS_ALLOWED_ACE
	_ = windows.GetAce(dacl, 0, &ace)
	ace.Header.AceSize++
	_, err = openLockFile(os.NewFile(9, "root"), "hash2.lock", sid)
	windowsLockTestAssert(t, errors.Is(err, errUnsafeWindowsLockState) && errors.Is(err, closeErr) && wraps == 2 && closes == 1, "refusal: err=%v wraps=%d closes=%d", err, wraps, closes)
}
func windowsLockTestUnchangedACL(t *testing.T, path string) func(*testing.T) {
	before, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	windowsLockTestFatal(t, err)
	return func(t *testing.T) {
		after, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
		windowsLockTestAssert(t, err == nil && before.String() == after.String(), "existing ACL changed: before=%q after=%q err=%v", before.String(), after.String(), err)
	}
}
func windowsLockTestUnchangedObject(t *testing.T, path, target string, reparse bool) func(*testing.T) {
	before, err := os.Lstat(path)
	windowsLockTestFatal(t, err)
	beforeTarget, beforeData := before, ""
	if target != "" {
		beforeTarget, err = os.Lstat(target)
		windowsLockTestFatal(t, err)
		data, readErr := os.ReadFile(target)
		windowsLockTestFatal(t, readErr)
		beforeData = string(data)
	}
	beforeLink := ""
	if reparse {
		beforeLink, err = os.Readlink(path)
		windowsLockTestFatal(t, err)
	}
	return func(t *testing.T) {
		after, err := os.Lstat(path)
		windowsLockTestFatal(t, err)
		same := before.IsDir() == after.IsDir() && os.SameFile(before, after)
		if target != "" {
			afterTarget, statErr := os.Lstat(target)
			windowsLockTestFatal(t, statErr)
			data, readErr := os.ReadFile(target)
			windowsLockTestFatal(t, readErr)
			same = same && os.SameFile(beforeTarget, afterTarget) && os.SameFile(after, afterTarget) && beforeData == string(data)
		}
		if reparse {
			link, readErr := os.Readlink(path)
			same = same && readErr == nil && link == beforeLink
		}
		windowsLockTestAssert(t, same, "existing object changed: %q", path)
	}
}
func TestWindowsLockOpenLockFileFilesystemCases(t *testing.T) {
	sid, err := currentWindowsLockSID()
	windowsLockTestFatal(t, err)
	root, err := openPrivateRoot(filepath.Join(t.TempDir(), "root"))
	windowsLockTestFatal(t, err)
	defer root.Close()
	for _, name := range []string{"success", "hard-link", "reparse", "type", "security"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(root.Name(), name+".lock")
			var unchanged func(*testing.T)
			switch name {
			case "hard-link", "reparse":
				target := path + ".target"
				windowsLockTestFatal(t, os.WriteFile(target, []byte("untouched"), 0o600))
				if name == "hard-link" {
					if err := os.Link(target, path); err != nil {
						t.Skipf("hard links unavailable: %v", err)
					}
				} else if err := os.Symlink(target, path); err != nil {
					t.Skipf("file symlinks unavailable: %v", err)
				}
				unchanged = windowsLockTestUnchangedObject(t, path, target, name == "reparse")
			case "type":
				windowsLockTestFatal(t, os.Mkdir(path, 0o700))
				unchanged = windowsLockTestUnchangedObject(t, path, "", false)
			case "security":
				windowsLockTestFatal(t, os.WriteFile(path, []byte("unsafe"), 0o600))
				unchanged = windowsLockTestUnchangedACL(t, path)
			}
			file, err := openLockFile(root, filepath.Base(path), sid)
			if name == "success" {
				windowsLockTestAssert(t, err == nil && file != nil, "openLockFile error = %v", err)
				windowsLockTestFatal(t, file.Close())
				return
			}
			if err == nil {
				_ = file.Close()
				t.Fatal("unsafe existing object opened")
			}
			unchanged(t)
		})
	}
}
func TestWindowsLockFoundationMissingParentFailsClosed(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing", "root")
	_, err := openPrivateRoot(root)
	windowsLockTestAssert(t, errors.Is(err, ErrInvalidRoot), "error = %v, want invalid root", err)
	_, err = os.Stat(root)
	windowsLockTestAssert(t, os.IsNotExist(err), "missing parent path changed: %v", err)
}
func TestWindowsLockFoundationRefusesUnsafeExistingRootUnchanged(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	windowsLockTestFatal(t, os.Mkdir(root, 0o700))
	before, err := windows.GetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	windowsLockTestFatal(t, err)
	_, err = openPrivateRoot(root)
	windowsLockTestAssert(t, errors.Is(err, ErrInvalidRoot), "error = %v, want invalid root", err)
	after, err := windows.GetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	windowsLockTestAssert(t, err == nil && before.String() == after.String(), "existing ACL changed: before=%q after=%q err=%v", before.String(), after.String(), err)
}
