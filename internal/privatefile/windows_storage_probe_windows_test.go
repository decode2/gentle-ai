//go:build windows && privatefilestorageprobe

package privatefile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsManagedStorageProbe(t *testing.T) {
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		b, err := os.ReadFile(sentinel)
		if err != nil || string(b) != "untouched" {
			t.Errorf("outside sentinel changed: %q, %v", b, err)
		}
	})

	// Inject a KnownFolderPath on the disposable fixture drive, not a profile.
	knownFolder := filepath.Join(t.TempDir(), "LocalAppData")
	if err := os.Mkdir(knownFolder, 0o700); err != nil {
		t.Fatal(err)
	}
	drive := strings.ToUpper(knownFolder[:2])

	// Inject facts, including a nominal-looking set. Even those facts must
	// BLOCK: neither equal DOS strings nor FileIdInfo identify their referents.
	valid := probeStorageFacts{
		before: []string{`\Device\HarddiskVolume1`}, after: []string{`\Device\HarddiskVolume1`},
		fsName: "NTFS", fsFlags: windows.FILE_PERSISTENT_ACLS,
		device: probeDeviceResult{size: 8, deviceType: 7, status: 0, ioStatus: 0, information: 8},
		fileID: [16]byte{1},
	}
	for _, tc := range []struct {
		name, want string
		change     func(*probeStorageFacts)
	}{
		{"identity contract missing", "identity contract", func(*probeStorageFacts) {}},
		{"mapping swapped", "mapping changed", func(f *probeStorageFacts) { f.after = []string{`\Device\HarddiskVolume2`} }},
		{"mapping after empty", "single direct", func(f *probeStorageFacts) { f.after = nil }},
		{"mapping after multiple", "single direct", func(f *probeStorageFacts) { f.after = append(f.after, `\Device\HarddiskVolume2`) }},
		{"mapping after non-direct", "single direct", func(f *probeStorageFacts) { f.after = []string{`\??\C:\alias`} }},
		{"mapping query error", "mapping query", func(f *probeStorageFacts) { f.mappingErr = errors.New("injected") }},
		{"mapping empty", "single direct", func(f *probeStorageFacts) { f.before = nil }},
		{"mapping multiple", "single direct", func(f *probeStorageFacts) { f.before = append(f.before, `\Device\HarddiskVolume2`) }},
		{"mapping non-direct", "single direct", func(f *probeStorageFacts) { f.before = []string{`\Device\Mup\share`} }},
		{"filesystem error", "filesystem query", func(f *probeStorageFacts) { f.fsErr = errors.New("injected") }},
		{"filesystem truncated", "filesystem name", func(f *probeStorageFacts) { f.fsComplete = false }},
		{"filesystem empty", "filesystem name", func(f *probeStorageFacts) { f.fsName = "" }},
		{"filesystem not NTFS", "unsupported filesystem", func(f *probeStorageFacts) { f.fsName = "ReFS" }},
		{"ACL flag missing", "persistent ACL", func(f *probeStorageFacts) { f.fsFlags = 0 }},
		{"missing API", "device query", func(f *probeStorageFacts) { f.device.err = errors.New("missing ntdll export") }},
		{"missing class contract", "device query", func(f *probeStorageFacts) { f.device.err = errors.New("missing user-mode class contract") }},
		{"short ABI buffer", "device ABI", func(f *probeStorageFacts) { f.device.size = 7 }},
		{"short information", "device ABI", func(f *probeStorageFacts) { f.device.information = 7 }},
		{"oversized information", "device ABI", func(f *probeStorageFacts) { f.device.information = 9 }},
		{"NTSTATUS failure", "device status", func(f *probeStorageFacts) { f.device.status = 0xC0000001 }},
		{"IO status disagreement", "device status", func(f *probeStorageFacts) { f.device.ioStatus = 0xC0000001 }},
		{"remote", "device signal", func(f *probeStorageFacts) { f.device.characteristics = 0x10 }},
		{"virtual", "device signal", func(f *probeStorageFacts) { f.device.deviceType = 0x24 }},
		{"contradictory", "device signal", func(f *probeStorageFacts) { f.device.deviceType, f.device.characteristics = 0x24, 0x10 }},
		{"unknown device", "device signal", func(f *probeStorageFacts) { f.device.deviceType = 0xffffffff }},
		{"unknown characteristic", "device signal", func(f *probeStorageFacts) { f.device.characteristics = 0x80000000 }},
		{"FileIdInfo unavailable", "handle identity", func(f *probeStorageFacts) { f.idErr = errors.New("unsupported") }},
		{"zero FileIdInfo", "handle identity", func(f *probeStorageFacts) { f.fileID = [16]byte{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := valid
			f.fsComplete = true
			tc.change(&f)
			if err := validateStorageProbe(f); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q refusal, got %v", tc.want, err)
			}
		})
	}

	t.Run("held root observations remain blocked", func(t *testing.T) {
		h, _, err := openWindowsManagedRootCandidate(knownFolder, queryWindowsManagedDevice, openWindowsManagedVolume)
		if err != nil {
			t.Fatalf("disposable drive has no direct root candidate: %v", err)
		}
		defer windows.CloseHandle(h)
		facts := queryWindowsStorageProbe(h, drive) // test-local query helper: TDD compile RED
		t.Logf("held-root FS=%q flags=%#x complete=%v FileIdInfo=%v mapping stable=%v device=%v (no physical-locality claim)",
			facts.fsName, facts.fsFlags, facts.fsComplete, facts.idErr == nil,
			facts.mappingErr == nil && len(facts.before) == 1 && len(facts.after) == 1 && facts.before[0] == facts.after[0], facts.device.err)
		if err := validateStorageProbe(facts); err != nil {
			t.Fatalf("BLOCK: held root storage probe: %v", err)
		}
		t.Fatal("BLOCK: missing mapping-to-held-root identity contract")
	})
}

// Facts remain observations only; no injected value grants a trusted root.
type probeDeviceResult struct {
	size                        uint32
	deviceType, characteristics uint32
	status, ioStatus            uint32
	information                 uintptr
	err                         error
}

type probeStorageFacts struct {
	before, after []string
	mappingErr    error
	fsName        string
	fsFlags       uint32
	fsComplete    bool
	fsErr         error
	device        probeDeviceResult
	fileID        [16]byte
	idErr         error
}

func validateStorageProbe(f probeStorageFacts) error {
	if f.mappingErr != nil {
		return fmt.Errorf("mapping query: %w", f.mappingErr)
	}
	if len(f.before) != 1 || len(f.after) != 1 || !windowsManagedDirectVolume(f.before[0]) || !windowsManagedDirectVolume(f.after[0]) {
		return fmt.Errorf("mapping is not a single direct volume")
	}
	if f.before[0] != f.after[0] {
		return fmt.Errorf("mapping changed while handle held")
	}
	if f.fsErr != nil {
		return fmt.Errorf("filesystem query: %w", f.fsErr)
	}
	if !f.fsComplete || f.fsName == "" {
		return fmt.Errorf("filesystem name incomplete")
	}
	if f.fsName != "NTFS" {
		return fmt.Errorf("unsupported filesystem: %q", f.fsName)
	}
	if f.fsFlags&windows.FILE_PERSISTENT_ACLS == 0 {
		return fmt.Errorf("persistent ACL flag missing")
	}
	if f.device.err != nil {
		return fmt.Errorf("device query: %w", f.device.err)
	}
	// FILE_FS_DEVICE_INFORMATION is two ULONGs (Microsoft wdm.h). x/sys
	// IO_STATUS_BLOCK models NTSTATUS + pointer-sized Information (ntifs.h).
	// No output is interpreted unless both completion statuses and byte count
	// agree exactly; on windows/amd64 the latter is 16 bytes at offset 8.
	if unsafe.Sizeof(windows.IO_STATUS_BLOCK{}) != 16 || unsafe.Offsetof(windows.IO_STATUS_BLOCK{}.Information) != 8 ||
		unsafe.Sizeof(probeFSDeviceInformation{}) != 8 || unsafe.Offsetof(probeFSDeviceInformation{}.characteristics) != 4 ||
		f.device.size != 8 || f.device.information != 8 {
		return fmt.Errorf("device ABI size/layout/information mismatch")
	}
	if f.device.status != 0 || f.device.ioStatus != 0 {
		return fmt.Errorf("device status mismatch: NT=%#x IO=%#x", f.device.status, f.device.ioStatus)
	}
	// Microsoft WinSDK shared/devioctl.h: FILE_DEVICE_DISK=0x07,
	// FILE_DEVICE_VIRTUAL_DISK=0x24 (win32metadata RecompiledIdlHeaders).
	// Refuse *all* characteristics (including FILE_REMOTE_DEVICE); allowing
	// unknown bits as benign would misclassify remote/virtual devices.
	if f.device.deviceType != 0x07 || f.device.characteristics != 0 {
		return fmt.Errorf("device signal unsupported: type=%#x characteristics=%#x", f.device.deviceType, f.device.characteristics)
	}
	if f.idErr != nil || f.fileID == [16]byte{} {
		return fmt.Errorf("handle identity unavailable: %v", f.idErr)
	}
	// FileIdInfo is handle-bound, not a comparable QueryDosDevice identifier.
	// Equal strings or volume serials cannot establish this missing contract.
	return fmt.Errorf("BLOCK: no mapping-to-held-root identity contract")
}

type probeFSDeviceInformation struct{ deviceType, characteristics uint32 }

func queryWindowsStorageProbe(h windows.Handle, drive string) probeStorageFacts {
	var f probeStorageFacts
	f.before, f.mappingErr = queryWindowsManagedDevice(drive)
	name := make([]uint16, 64)
	f.fsErr = windows.GetVolumeInformationByHandle(h, nil, 0, nil, nil, &f.fsFlags, &name[0], uint32(len(name)))
	if f.fsErr == nil {
		for i, unit := range name {
			if unit == 0 {
				f.fsComplete = i > 0
				f.fsName = windows.UTF16ToString(name[:i])
				break
			}
		}
	}
	// winbase.h FILE_ID_INFO: ULONGLONG VolumeSerialNumber; FILE_ID_128
	// FileId. The serial is not an identity comparison with a DOS mapping.
	var id [24]byte
	f.idErr = windows.GetFileInformationByHandleEx(h, windows.FileIdInfo, &id[0], uint32(len(id)))
	if f.idErr == nil {
		copy(f.fileID[:], id[8:])
	}
	var err error
	f.after, err = queryWindowsManagedDevice(drive)
	if err != nil {
		f.mappingErr = errors.Join(f.mappingErr, err)
	}
	// Microsoft documents NtQueryVolumeInformationFile's class and structure
	// in ntifs.h but lists NtosKrnl.exe/NtosKrnl.lib (driver DDI), not an
	// ntdll.dll user-mode export/ABI guarantee. Do not call a lucky export.
	// Device negatives above test the exact acceptance boundary by injection;
	// this real measurement must BLOCK until a safe user-mode contract exists.
	f.device.err = errors.New("missing documented ntdll user-mode FileFsDeviceInformation contract")
	return f
}
