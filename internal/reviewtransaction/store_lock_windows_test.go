//go:build windows

package reviewtransaction

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNTPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "drive path",
			path: `C:\review-store\LOCK`,
			want: `\??\C:\review-store\LOCK`,
		},
		{
			name: "UNC path",
			path: `\\server\share\review-store\LOCK`,
			want: `\??\UNC\server\share\review-store\LOCK`,
		},
		{
			name: "extended drive path",
			path: `\\?\C:\review-store\LOCK`,
			want: `\??\C:\review-store\LOCK`,
		},
		{
			name: "extended UNC path",
			path: `\\?\UNC\server\share\review-store\LOCK`,
			want: `\??\UNC\server\share\review-store\LOCK`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ntPath(tt.path); got != tt.want {
				t.Fatalf("ntPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestWindowsSecureStoreLockRejectsReparsePointAndPreservesTarget(t *testing.T) {
	target := filepath.Join(t.TempDir(), "external-target")
	want := []byte("external data must not be lock metadata\n")
	if err := os.WriteFile(target, want, 0o600); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "review-store", "LOCK")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("creating a file symlink is unavailable: %v", err)
	}

	if _, err := acquireLocalStoreLock(path); err == nil {
		t.Fatal("acquireLocalStoreLock(reparse point) succeeded")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("external target changed: got %q, want %q", got, want)
	}
}

func TestWindowsSecureStoreLockRejectsDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review-store", "LOCK")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireLocalStoreLock(path); err == nil {
		t.Fatal("acquireLocalStoreLock(directory) succeeded")
	}
}

func TestWindowsStoreLockUsesExistingInodeAdvisoryTruth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review-store", "LOCK")
	held, err := acquireStoreLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireStoreLock(path); !errors.Is(err, ErrConcurrentUpdate) {
		t.Fatalf("second Windows advisory acquisition = %v, want ErrConcurrentUpdate", err)
	}
	if evidence, exists := inventoryLock(AuthorityVersionCompact, "", path); !exists || evidence.Status != AuthorityLockOwned || evidence.Owner != nil {
		t.Fatalf("busy Windows lock evidence = %#v, exists=%t", evidence, exists)
	}
	if err := held.release(); err != nil {
		t.Fatal(err)
	}
	if evidence, exists := inventoryLock(AuthorityVersionCompact, "", path); !exists || evidence.Status != AuthorityLockReleased || evidence.Owner != nil {
		t.Fatalf("released Windows lock evidence = %#v, exists=%t", evidence, exists)
	}
}
