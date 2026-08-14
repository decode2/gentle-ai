//go:build windows

package reviewtransaction

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func recordLock(t *testing.T, a *RecordStorageAuthority) *RecordStorageLock {
	t.Helper()
	l, err := OpenRecordStorageLock(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestRecordStorageLockRetainsPrivateStaleFile(t *testing.T) {
	a, lease := createAuthority(t)
	first := recordLock(t, a)
	if !validRecordStorageLock(first.data) || !privateSecureWindowsData(first.data.handle) {
		t.Fatal("lock is not private regular local data")
	}
	id := first.data.id
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if second := recordLock(t, a); second.data.id != id {
		t.Fatal("stale lock identity changed")
	}
	p := filepath.Join(lease.Identity().GitCommonDir, recordStorageGentleAI, recordStorageRecords, lease.StorageKey(), recordStorageLockName)
	if info, err := os.Stat(p); err != nil || !info.Mode().IsRegular() || info.Size() != 0 {
		t.Fatalf("lock = %v, %v", info, err)
	}
}

func TestRecordStorageLockContentionCancellationAndRelease(t *testing.T) {
	a, lease := createAuthority(t)
	first, second := recordLock(t, a), recordLock(t, createAuthorityForLease(t, lease))
	held, err := first.Lock(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() { _, err := second.Lock(ctx); result <- err }()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not complete")
	}
	if err := held(); err != nil {
		t.Fatal(err)
	}
	unlock, err := second.Lock(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
}

func TestRecordStorageLockRefusesHostileAndZeroValues(t *testing.T) {
	for _, make := range []func(*testing.T, string){func(t *testing.T, p string) {
		if err := os.Mkdir(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}, func(t *testing.T, p string) {
		if err := os.Symlink(t.TempDir(), p); err != nil {
			t.Skip(err)
		}
	}} {
		a, lease := createAuthority(t)
		p := filepath.Join(lease.Identity().GitCommonDir, recordStorageGentleAI, recordStorageRecords, lease.StorageKey(), recordStorageLockName)
		make(t, p)
		if l, err := OpenRecordStorageLock(t.Context(), a); l != nil || !errors.Is(err, errRecordStorageUnavailable) {
			t.Fatalf("hostile = %v, %v", l, err)
		}
	}
	for _, lock := range []*RecordStorageLock{nil, &RecordStorageLock{}} {
		if _, err := lock.Lock(t.Context()); !errors.Is(err, errRecordStorageUnavailable) {
			t.Fatalf("zero lock = %v", err)
		}
		if err := lock.Close(); lock != nil && !errors.Is(err, errRecordStorageUnavailable) {
			t.Fatalf("zero close = %v", err)
		}
	}
}
