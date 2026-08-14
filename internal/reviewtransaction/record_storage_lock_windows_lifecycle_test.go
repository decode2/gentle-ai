//go:build windows

package reviewtransaction

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestRecordStorageLockCloseAndNamespaces(t *testing.T) {
	a, lease := createAuthority(t)
	lock := recordLock(t, a)
	unlock, err := lock.Lock(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- lock.Close() }()
	select {
	case err := <-closed:
		t.Fatalf("close during lease = %v", err)
	default:
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not finish")
	}
	if _, err := lock.Lock(t.Context()); !errors.Is(err, errRecordStorageUnavailable) {
		t.Fatalf("post-close = %v", err)
	}
	other, err := OpenRecordStorageAuthority(t.Context(), lease, strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	first, second := recordLock(t, a), recordLock(t, other)
	held, err := first.Lock(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		release, err := second.Lock(t.Context())
		if err == nil {
			err = release()
		}
		result <- err
	}()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("digest namespaces contended")
	}
	if err := held(); err != nil {
		t.Fatal(err)
	}
}

func TestRecordStorageLockRefusesExistingWeakACLWithoutRepair(t *testing.T) {
	authority, lease := createAuthority(t)
	first := recordLock(t, authority)
	weakenAuthorityACL(t, first.data.handle)
	if privateSecureWindowsData(first.data.handle) {
		t.Fatal("weak ACL mutation did not take effect")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	lock, err := OpenRecordStorageLock(t.Context(), authority)
	if lock != nil || !errors.Is(err, errRecordStorageUnavailable) {
		t.Fatalf("weak lock = %v, %v", lock, err)
	}
	for _, secret := range []string{lease.Identity().GitCommonDir, lease.StorageKey(), recordStorageLockName, os.Getenv("HOME")} {
		if secret != "" && strings.Contains(err.Error(), secret) {
			t.Fatalf("unredacted error = %v", err)
		}
	}
}

func TestRecordStorageLockRetainedHandlePreventsReplacementOrDetectsIt(t *testing.T) {
	authority, lease := createAuthority(t)
	lock := recordLock(t, authority)
	path := filepath.Join(lease.Identity().GitCommonDir, recordStorageGentleAI, recordStorageRecords, lease.StorageKey(), recordStorageLockName)
	if err := os.Rename(path, path+"-old"); err == nil {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := lock.Lock(t.Context()); !errors.Is(err, errRecordStorageUnavailable) {
			t.Fatalf("replaced lock = %v", err)
		}
		return
	} else if !errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatal(err)
	}
	if err := os.Remove(path); err == nil {
		t.Fatal("retained lock handle allowed deletion")
	} else if !errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatal(err)
	}
	release, err := lock.Lock(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestRecordStorageLockReleaseAndCloseAreIdempotentUnderConcurrency(t *testing.T) {
	authority, _ := createAuthority(t)
	lock := recordLock(t, authority)
	release, err := lock.Lock(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() { defer group.Done(); results <- release() }()
	}
	group.Wait()
	close(results)
	var available, unavailable int
	for err := range results {
		if err == nil {
			available++
		} else if errors.Is(err, errRecordStorageUnavailable) {
			unavailable++
		} else {
			t.Fatal(err)
		}
	}
	if available != 1 || unavailable != 1 {
		t.Fatalf("release results = %d available, %d unavailable", available, unavailable)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRecordStorageLockRejectsAuthorityDrift(t *testing.T) {
	authority, _ := createAuthority(t)
	lock := recordLock(t, authority)
	authority.storageKey = strings.Repeat("0", 64)
	if _, err := lock.Lock(t.Context()); !errors.Is(err, errRecordStorageAuthorityInvalid) {
		t.Fatalf("authority drift = %v", err)
	}
}
