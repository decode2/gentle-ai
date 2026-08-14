//go:build windows

package reviewtransaction

import (
	"context"
	"errors"
	"io/fs"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	errWindowsRelativePublishInvalid     = errors.New("Windows relative publication is invalid")         // refusal:by-design operator-knowledge: the caller must provide live local handles, a safe child name, and delete/synchronize source access
	errWindowsRelativePublishUnsupported = errors.New("Windows relative publication is unsupported")     // refusal:by-design world-action: a remote or cross-volume namespace cannot provide this atomic handle-relative publication
	errWindowsRelativePublishUnknown     = errors.New("Windows relative publication outcome is unknown") // refusal:by-design human-authority: post-publication identity verification failed, so a caller must reconcile before treating publication as complete
)

type windowsRenameInformation struct {
	ReplaceIfExists uint32
	RootDirectory   windows.Handle
	FileNameLength  uint32
	FileName        [1]uint16
}

const windowsFileRemoteDevice = 0x10

// publishWindowsNoReplaceRelative atomically publishes source as one child of destinationDir without a durability claim.
func publishWindowsNoReplaceRelative(source, destinationDir windows.Handle, destinationName string) error {
	return publishWindowsRelativeAfter(source, destinationDir, destinationName, false, secureWindowsChildID{}, nil)
}

// afterRename is test-only observation injection; publication may have occurred when it fails verification.
func publishWindowsNoReplaceRelativeAfter(source, destinationDir windows.Handle, destinationName string, afterRename func()) error {
	return publishWindowsRelativeAfter(source, destinationDir, destinationName, false, secureWindowsChildID{}, afterRename)
}

// publishWindowsReplaceRelative atomically replaces a retained child entry.
func publishWindowsReplaceRelative(source, destinationDir windows.Handle, destinationName string, expected secureWindowsChildID) error {
	return publishWindowsRelativeAfter(source, destinationDir, destinationName, true, expected, nil)
}

func publishWindowsRelativeAfter(source, destinationDir windows.Handle, destinationName string, replace bool, expected secureWindowsChildID, afterRename func()) error {
	if !secureWindowsChildName(destinationName) {
		return errWindowsRelativePublishInvalid
	}
	sourceInfo, sourceID, ok := secureWindowsDataInfo(source)
	if !ok || !windowsRenameAccess(source) || !localWindowsDiskHandle(source) {
		return errWindowsRelativePublishInvalid
	}
	_, destinationID, ok := secureWindowsDirectoryInfo(destinationDir)
	if !ok || !localWindowsDiskHandle(destinationDir) {
		return errWindowsRelativePublishInvalid
	}
	if sourceInfo.VolumeSerialNumber != destinationID.volume {
		return errWindowsRelativePublishUnsupported
	}
	name, err := windows.UTF16FromString(destinationName)
	if err != nil || len(name) < 2 || len(name) > 32768 {
		return errWindowsRelativePublishInvalid
	}
	var layout windowsRenameInformation
	length := int(unsafe.Offsetof(layout.FileName)) + 2*(len(name)-1)
	buffer := make([]byte, length)
	rename := (*windowsRenameInformation)(unsafe.Pointer(&buffer[0]))
	if replace {
		rename.ReplaceIfExists = 1
	}
	rename.RootDirectory, rename.FileNameLength = destinationDir, uint32(2*(len(name)-1))
	copy((*[32767]uint16)(unsafe.Pointer(&rename.FileName[0]))[:len(name)-1], name)
	var current *secureWindowsData
	if replace {
		current, err = openSecureWindowsChildData(context.Background(), destinationDir, &destinationID, destinationName)
		if err != nil {
			return err
		}
		if !current.valid() || !localWindowsDiskHandle(current.handle) || current.id != expected {
			if current != nil {
				_ = current.Close()
			}
			return errWindowsRelativePublishInvalid
		}
	}
	if _, id, valid := secureWindowsDataInfo(source); !valid || id != sourceID || !secureWindowsDirectoryHandle(destinationDir, &destinationID) {
		if current != nil {
			_ = current.Close()
		}
		return errWindowsRelativePublishInvalid
	}
	var status windows.IO_STATUS_BLOCK
	err = windows.NtSetInformationFile(source, &status, &buffer[0], uint32(length), windows.FileRenameInformation)
	if err != nil {
		if current != nil {
			_ = current.Close()
		}
		return windowsRelativePublishError(err)
	}
	if current != nil && current.Close() != nil {
		return errWindowsRelativePublishUnknown
	}
	if afterRename != nil {
		afterRename()
	}
	if _, id, valid := secureWindowsDataInfo(source); !valid || id != sourceID || !secureWindowsDirectoryHandle(destinationDir, &destinationID) {
		return errWindowsRelativePublishUnknown
	}
	published, err := openWindowsRelativeData(destinationDir, destinationName)
	if err != nil || published != sourceID {
		return errWindowsRelativePublishUnknown
	}
	return nil
}

func windowsRenameAccess(handle windows.Handle) bool {
	var status windows.IO_STATUS_BLOCK
	var access uint32
	if windows.NtQueryInformationFile(handle, &status, (*byte)(unsafe.Pointer(&access)), uint32(unsafe.Sizeof(access)), 8) != nil {
		return false
	}
	return access&(uint32(windows.DELETE)|uint32(windows.SYNCHRONIZE)) == uint32(windows.DELETE)|uint32(windows.SYNCHRONIZE)
}

func localWindowsDiskHandle(handle windows.Handle) bool {
	var flags uint32
	return windows.GetVolumeInformationByHandle(handle, nil, 0, nil, nil, &flags, nil, 0) == nil && flags&windowsFileRemoteDevice == 0
}

func openWindowsRelativeData(parent windows.Handle, name string) (secureWindowsChildID, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return secureWindowsChildID{}, errWindowsRelativePublishInvalid
	}
	attributes := &windows.OBJECT_ATTRIBUTES{Length: uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})), RootDirectory: parent, ObjectName: objectName, Attributes: windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE}
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&handle, windows.FILE_GENERIC_READ|windows.SYNCHRONIZE, attributes, &status, nil, windows.FILE_ATTRIBUTE_NORMAL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT, 0, 0)
	if err != nil {
		return secureWindowsChildID{}, err
	}
	defer windows.CloseHandle(handle)
	_, id, ok := secureWindowsDataInfo(handle)
	if !ok {
		return secureWindowsChildID{}, errWindowsRelativePublishInvalid
	}
	return id, nil
}

func windowsRelativePublishError(err error) error {
	if errors.Is(err, windows.STATUS_OBJECT_NAME_COLLISION) {
		return fs.ErrExist
	}
	if errors.Is(err, windows.STATUS_NOT_SAME_DEVICE) {
		return errWindowsRelativePublishUnsupported
	}
	return errWindowsRelativePublishInvalid
}
