//go:build windows

package reviewtransaction

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPublishWindowsNoReplaceRelativePublishesInRetainedDirectory(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	for _, path := range []string{first, second} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	destination := publishDirectory(t, first)
	defer windows.CloseHandle(destination)
	sourcePath := filepath.Join(root, "source")
	source := publishSource(t, sourcePath, []byte("winner"))
	defer windows.CloseHandle(source)
	if err := publishWindowsNoReplaceRelative(source, destination, "record"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sourcePath); !os.IsNotExist(err) {
		t.Fatalf("source after rename = %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(first, "record")); err != nil || string(got) != "winner" {
		t.Fatalf("published = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(second, "record")); !os.IsNotExist(err) {
		t.Fatalf("other directory changed: %v", err)
	}
	otherSource := publishSource(t, filepath.Join(root, "other-source"), []byte("other"))
	defer windows.CloseHandle(otherSource)
	otherDestination := publishDirectory(t, second)
	defer windows.CloseHandle(otherDestination)
	if err := publishWindowsNoReplaceRelative(otherSource, otherDestination, "record"); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(second, "record")); err != nil || string(got) != "other" {
		t.Fatalf("second publication = %q, %v", got, err)
	}
}

func TestPublishWindowsNoReplaceRelativeConflictAndRace(t *testing.T) {
	dir := t.TempDir()
	destination := publishDirectory(t, dir)
	defer windows.CloseHandle(destination)
	paths := [2]string{filepath.Join(dir, "first"), filepath.Join(dir, "second")}
	bytes := [2][]byte{[]byte("first"), []byte("second")}
	handles := [2]windows.Handle{publishSource(t, paths[0], bytes[0]), publishSource(t, paths[1], bytes[1])}
	defer windows.CloseHandle(handles[0])
	defer windows.CloseHandle(handles[1])
	var ids [2]secureWindowsChildID
	_, ids[0], _ = secureWindowsDataInfo(handles[0])
	_, ids[1], _ = secureWindowsDataInfo(handles[1])
	var results [2]error
	var group sync.WaitGroup
	for index, source := range handles {
		group.Add(1)
		go func(i int, h windows.Handle) {
			defer group.Done()
			results[i] = publishWindowsNoReplaceRelative(h, destination, "record")
		}(index, source)
	}
	group.Wait()
	if (results[0] == nil) == (results[1] == nil) || !(errors.Is(results[0], os.ErrExist) || errors.Is(results[1], os.ErrExist)) {
		t.Fatalf("race results = %v", results)
	}
	winner := 0
	if results[1] == nil {
		winner = 1
	}
	loser := 1 - winner
	if _, err := os.Stat(paths[winner]); !os.IsNotExist(err) {
		t.Fatalf("winner source = %v", err)
	}
	if id, err := openWindowsRelativeData(destination, filepath.Base(paths[loser])); err != nil || id != ids[loser] {
		t.Fatalf("loser namespace identity = %v, %v", id, err)
	}
	if got, err := os.ReadFile(paths[loser]); err != nil || string(got) != string(bytes[loser]) {
		t.Fatalf("loser = %q, %v", got, err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "record"))
	if err != nil || string(got) != string(bytes[winner]) {
		t.Fatalf("winner = %q, %v", got, err)
	}
}

func TestPublishWindowsNoReplaceRelativeRejectsUntrustedInputs(t *testing.T) {
	dir := t.TempDir()
	destination := publishDirectory(t, dir)
	defer windows.CloseHandle(destination)
	source := publishSource(t, filepath.Join(dir, "source"), []byte("data"))
	defer windows.CloseHandle(source)
	for _, name := range []string{"", ".", "..", "x/y", `x\y`, "x:y", "x.", "NUL"} {
		mustRelativeInvalid(t, publishWindowsNoReplaceRelative(source, destination, name))
	}
	mustRelativeInvalid(t, publishWindowsNoReplaceRelative(destination, destination, "record"))
	fileDestination := publishSource(t, filepath.Join(dir, "not-directory"), nil)
	defer windows.CloseHandle(fileDestination)
	mustRelativeInvalid(t, publishWindowsNoReplaceRelative(source, fileDestination, "record"))
	closed := publishDirectory(t, t.TempDir())
	_ = windows.CloseHandle(closed)
	mustRelativeInvalid(t, publishWindowsNoReplaceRelative(source, closed, "record"))
	weakSource := publishFile(t, filepath.Join(dir, "weak"), nil, windows.GENERIC_READ|windows.SYNCHRONIZE)
	defer windows.CloseHandle(weakSource)
	mustRelativeInvalid(t, publishWindowsNoReplaceRelative(weakSource, destination, "record"))
	deviceName, _ := windows.UTF16PtrFromString("NUL")
	device, deviceErr := windows.CreateFile(deviceName, windows.GENERIC_READ|windows.SYNCHRONIZE, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
	if deviceErr != nil {
		t.Fatal(deviceErr)
	}
	defer windows.CloseHandle(device)
	mustRelativeInvalid(t, publishWindowsNoReplaceRelative(device, destination, "record"))
	link := filepath.Join(dir, "junction")
	junctionAuthorityPath(t, link)
	reparse := publishDirectory(t, link)
	defer windows.CloseHandle(reparse)
	mustRelativeInvalid(t, publishWindowsNoReplaceRelative(source, reparse, "record"))
	mustRelativeInvalid(t, publishWindowsNoReplaceRelative(reparse, destination, "record"))
}

func TestPublishWindowsNoReplaceRelativeRetainsDirectoryIdentity(t *testing.T) {
	root := t.TempDir()
	original, moved := filepath.Join(root, "target"), filepath.Join(root, "moved")
	if err := os.Mkdir(original, 0o700); err != nil {
		t.Fatal(err)
	}
	destination := publishDirectory(t, original)
	defer windows.CloseHandle(destination)
	if err := os.Rename(original, moved); err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			if _, statErr := os.Stat(moved); !os.IsNotExist(statErr) {
				t.Fatal("namespace replaced")
			}
			return
		}
		t.Fatal(err)
	}
	if err := os.Mkdir(original, 0o700); err != nil {
		t.Fatal(err)
	}
	source := publishSource(t, filepath.Join(root, "source"), []byte("retained"))
	_, sourceID, ok := secureWindowsDataInfo(source)
	defer windows.CloseHandle(source)
	if !ok {
		t.Fatal("source handle invalid")
	}
	if err := publishWindowsNoReplaceRelative(source, destination, "record"); err != nil {
		t.Fatalf("retained directory publish = %v", err)
	}
	if id, err := openWindowsRelativeData(destination, "record"); err != nil || id != sourceID {
		t.Fatalf("relative identity = %v, %v", id, err)
	}
	if got, err := os.ReadFile(filepath.Join(moved, "record")); err != nil || string(got) != "retained" {
		t.Fatalf("retained directory = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(original, "record")); !os.IsNotExist(err) {
		t.Fatalf("replacement namespace changed: %v", err)
	}
}

func TestPublishWindowsNoReplaceRelativeReportsPostRenameVerificationFailure(t *testing.T) {
	dir := t.TempDir()
	destination := publishDirectory(t, dir)
	source := publishSource(t, filepath.Join(dir, "source"), []byte("published"))
	defer windows.CloseHandle(source)
	err := publishWindowsNoReplaceRelativeAfter(source, destination, "record", func() { _ = windows.CloseHandle(destination) })
	if !errors.Is(err, errWindowsRelativePublishUnknown) {
		t.Fatalf("post-rename verification = %v", err)
	}
	if got, readErr := os.ReadFile(filepath.Join(dir, "record")); readErr != nil || string(got) != "published" {
		t.Fatalf("published namespace = %q, %v", got, readErr)
	}
}

func TestPublishWindowsNoReplaceRelativeRejectsDifferentVolume(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source")
	source := publishSource(t, sourcePath, []byte("data"))
	defer windows.CloseHandle(source)
	_, sourceID, ok := secureWindowsDataInfo(source)
	if !ok {
		t.Fatal("source handle is invalid")
	}
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		t.Fatal(err)
	}
	for index := uint(0); index < 26; index++ {
		if mask&(1<<index) == 0 {
			continue
		}
		root := string(rune('A'+index)) + `:\`
		name, nameErr := windows.UTF16PtrFromString(root)
		var serial uint32
		if nameErr != nil || windows.GetVolumeInformation(name, nil, 0, &serial, nil, nil, nil, 0) != nil || serial == sourceID.volume {
			continue
		}
		dir, makeErr := os.MkdirTemp(root, "gentle-ai-publish-*")
		if makeErr != nil {
			continue
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		destination := publishDirectory(t, dir)
		defer windows.CloseHandle(destination)
		if err := publishWindowsNoReplaceRelative(source, destination, "record"); !errors.Is(err, errWindowsRelativePublishUnsupported) {
			t.Fatalf("different volume = %v", err)
		}
		return
	}
	t.Skip("no writable second local volume")
}

func TestPublishWindowsNoReplaceRelativeErrorsRedactInputs(t *testing.T) {
	dir := t.TempDir()
	destination := publishDirectory(t, dir)
	defer windows.CloseHandle(destination)
	source := publishSource(t, filepath.Join(dir, "source"), nil)
	defer windows.CloseHandle(source)
	name := "secret:name"
	err := publishWindowsNoReplaceRelative(source, destination, name)
	if !errors.Is(err, errWindowsRelativePublishInvalid) || strings.Contains(err.Error(), dir) || strings.Contains(err.Error(), name) || strings.Contains(err.Error(), "0x") || strings.Contains(err.Error(), os.Getenv("HOME")) {
		t.Fatalf("leaked input: %q", err)
	}
}

func publishDirectory(t *testing.T, path string) windows.Handle {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.SYNCHRONIZE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	return handle
}

func publishSource(t *testing.T, path string, data []byte) windows.Handle {
	return publishFile(t, path, data, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.DELETE|windows.SYNCHRONIZE)
}

func publishFile(t *testing.T, path string, data []byte, access uint32) windows.Handle {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	return handle
}

func mustRelativeInvalid(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, errWindowsRelativePublishInvalid) {
		t.Fatalf("invalid input = %v", err)
	}
}
