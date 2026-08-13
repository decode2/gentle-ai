//go:build windows

package reviewtransaction

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/windows"
)

func TestSecureWindowsChildDirectoryCreateOpenAndClose(t *testing.T) {
	parent, handle := secureWindowsChildParent(t)
	child, err := createSecureWindowsChildDirectory(context.Background(), handle, nil, "direct-run-records")
	if err != nil {
		t.Fatal(err)
	}
	if !child.validLocked() || validatePrivateRARDirectory(filepath.Join(parent, "direct-run-records")) != nil {
		t.Fatal("child is not private")
	}
	id := child.id
	if err := child.Close(); err != nil {
		t.Fatal(err)
	}
	if err := child.Close(); err != nil {
		t.Fatal(err)
	}
	opened, err := openSecureWindowsChildDirectory(context.Background(), handle, nil, "direct-run-records")
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if opened.id != id {
		t.Fatal("child identity changed")
	}
	if _, err := createSecureWindowsChildDirectory(context.Background(), handle, nil, "direct-run-records"); !errors.Is(err, errSecureWindowsChildExists) {
		t.Fatalf("conflict = %v", err)
	}
	if _, err := openSecureWindowsChildDirectory(context.Background(), handle, nil, "missing"); !errors.Is(err, errSecureWindowsChildMissing) {
		t.Fatalf("missing = %v", err)
	}
}

func TestSecureWindowsChildRejectsUnsafeNamesBeforeAccess(t *testing.T) {
	parent, handle := secureWindowsChildParent(t)
	for _, name := range []string{"", ".", "..", "x/y", `x\y`, "x:y", "x\x00y", "x.", "x ", "CON", "com1", `\\?\\C:`, "C:child"} {
		t.Run(name, func(t *testing.T) {
			before, readErr := os.ReadDir(parent)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if _, err := createSecureWindowsChildDirectory(context.Background(), handle, nil, name); !errors.Is(err, errSecureWindowsChildInvalid) {
				t.Fatalf("error = %v", err)
			}
			after, readErr := os.ReadDir(parent)
			if readErr != nil || len(after) != len(before) {
				t.Fatalf("unsafe child changed parent: entries=%d error=%v", len(after), readErr)
			}
		})
	}
}

func TestSecureWindowsChildRejectsParentAndChildSubstitution(t *testing.T) {
	parent, handle := secureWindowsChildParent(t)
	file, err := os.Create(filepath.Join(parent, "file"))
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	if _, err := openSecureWindowsChildDirectory(context.Background(), handle, nil, "file"); !errors.Is(err, errSecureWindowsChildInvalid) {
		t.Fatalf("file = %v", err)
	}
	info, _, ok := secureWindowsDirectoryInfo(handle)
	if !ok {
		t.Fatal("parent handle invalid")
	}
	want := secureWindowsChildID{info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow + 1}
	if _, err := createSecureWindowsChildDirectory(context.Background(), handle, &want, "isolated"); !errors.Is(err, errSecureWindowsChildInvalid) {
		t.Fatalf("identity mismatch = %v", err)
	}
	if err := windows.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	if _, err := createSecureWindowsChildDirectory(context.Background(), handle, nil, "closed"); !errors.Is(err, errSecureWindowsChildInvalid) {
		t.Fatalf("closed = %v", err)
	}
}

func TestSecureWindowsChildRejectsReparse(t *testing.T) {
	parent, handle := secureWindowsChildParent(t)
	if err := os.Symlink(t.TempDir(), filepath.Join(parent, "link")); err != nil {
		t.Skipf("unprivileged reparse unavailable: %v", err)
	}
	if _, err := openSecureWindowsChildDirectory(context.Background(), handle, nil, "link"); !errors.Is(err, errSecureWindowsChildInvalid) {
		t.Fatalf("reparse = %v", err)
	}
}

func TestSecureWindowsChildRejectsWeakChildAndIsolatesParents(t *testing.T) {
	parent, handle := secureWindowsChildParent(t)
	weak := filepath.Join(parent, "weak")
	if err := os.Mkdir(weak, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := openSecureWindowsChildDirectory(context.Background(), handle, nil, "weak"); !errors.Is(err, errSecureWindowsChildInvalid) {
		t.Fatalf("weak ACL = %v", err)
	}
	other, otherHandle := secureWindowsChildParent(t)
	first, err := createSecureWindowsChildDirectory(context.Background(), handle, nil, "same")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := createSecureWindowsChildDirectory(context.Background(), otherHandle, nil, "same")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if first.id == second.id || filepath.Join(parent, "same") == filepath.Join(other, "same") {
		t.Fatal("different parents were not isolated")
	}
}

func TestSecureWindowsChildCloseIsConcurrent(t *testing.T) {
	_, handle := secureWindowsChildParent(t)
	child, err := createSecureWindowsChildDirectory(context.Background(), handle, nil, "child")
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() { defer group.Done(); _ = child.Close() }()
	}
	group.Wait()
	if child.validLocked() {
		t.Fatal("closed child remained valid")
	}
}

func TestSecureWindowsChildErrorsRedactAuthorityInputs(t *testing.T) {
	parent, handle := secureWindowsChildParent(t)
	name := "secret:record"
	_, err := createSecureWindowsChildDirectory(context.Background(), handle, nil, name)
	if !errors.Is(err, errSecureWindowsChildInvalid) {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), parent) || strings.Contains(err.Error(), name) || strings.Contains(err.Error(), "0x") {
		t.Fatalf("error leaked authority input: %q", err)
	}
}

func secureWindowsChildParent(t *testing.T) (string, windows.Handle) {
	t.Helper()
	parent := filepath.Join(t.TempDir(), "parent")
	if _, err := createPrivateRARDirectory(parent); err != nil {
		t.Fatal(err)
	}
	file, err := openRARPathNoFollow(parent, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return parent, windows.Handle(file.Fd())
}
