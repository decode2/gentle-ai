//go:build windows

package reviewtransaction

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/windows"
)

func TestRecordStorageAuthorityReplaceRecordStoresExactPrivateBytes(t *testing.T) {
	for _, test := range []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"small", []byte("replacement")},
		{"maximum", bytes.Repeat([]byte("x"), 1<<20)},
	} {
		t.Run(test.name, func(t *testing.T) {
			authority, lease := createAuthority(t)
			digest := createDigest(test.name)
			if err := authority.CreateRecord(t.Context(), digest, []byte("old")); err != nil {
				t.Fatal(err)
			}
			if err := authority.ReplaceRecord(t.Context(), digest, test.data); err != nil {
				t.Fatal(err)
			}
			got, err := authority.ReadRecord(t.Context(), digest, 1<<20)
			if err != nil || !bytes.Equal(got, test.data) {
				t.Fatalf("replacement = %q, %v", got, err)
			}
			path := filepath.Join(lease.Identity().GitCommonDir, recordStorageGentleAI, recordStorageRecords, lease.StorageKey(), digest)
			if err := validatePrivateRARFile(path); err != nil {
				t.Fatalf("replacement ACL = %v", err)
			}
			assertCreateNoTemps(t, filepath.Dir(path))
		})
	}
}
func TestRecordStorageAuthorityReplaceRecordRefusesUnsafeInputs(t *testing.T) {
	authority, lease := createAuthority(t)
	digest := createDigest("replace")
	dir := filepath.Join(lease.Identity().GitCommonDir, recordStorageGentleAI, recordStorageRecords, lease.StorageKey())
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, test := range []struct {
		name   string
		ctx    context.Context
		digest string
		data   []byte
		want   error
	}{
		{"invalid", t.Context(), strings.Repeat("A", 64), nil, errRecordStorageAuthorityInvalid},
		{"large", t.Context(), digest, make([]byte, 1<<20+1), errRecordStorageTooLarge},
		{"canceled", canceled, digest, nil, context.Canceled},
		{"missing", t.Context(), digest, nil, errRecordStorageMissing},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := authority.ReplaceRecord(test.ctx, test.digest, test.data); !errors.Is(err, test.want) {
				t.Fatalf("ReplaceRecord() = %v", err)
			}
			assertCreateNoTemps(t, dir)
		})
	}
}

func TestRecordStorageAuthorityReplaceRecordCopiesAndRechecksDestination(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{"directory", func(t *testing.T, path string) {
			_ = os.Remove(path)
			if _, err := createPrivateRARDirectory(path); err != nil {
				t.Fatal(err)
			}
		}},
		{"reparse", func(t *testing.T, path string) { _ = os.Remove(path); symlinkAuthorityPath(t, path) }},
		{"weak-acl", func(t *testing.T, path string) {
			h := openAuthorityACLHandle(t, path)
			defer windows.CloseHandle(h)
			weakenAuthorityACL(t, h)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			authority, lease := createAuthority(t)
			digest := createDigest(test.name)
			if err := authority.CreateRecord(t.Context(), digest, []byte("old")); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(lease.Identity().GitCommonDir, recordStorageGentleAI, recordStorageRecords, lease.StorageKey(), digest)
			test.setup(t, path)
			if err := authority.ReplaceRecord(t.Context(), digest, []byte("new")); !errors.Is(err, errRecordStorageAuthorityInvalid) {
				t.Fatalf("ReplaceRecord() = %v", err)
			}
			assertCreateNoTemps(t, filepath.Dir(path))
		})
	}
	authority, lease := createAuthority(t)
	digest := createDigest("swap")
	if err := authority.CreateRecord(t.Context(), digest, []byte("old")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(lease.Identity().GitCommonDir, recordStorageGentleAI, recordStorageRecords, lease.StorageKey(), digest)
	authority.replaceHook = func() {
		if err := os.Rename(path, path+"-old"); err != nil {
			t.Fatal(err)
		}
		file, err := createPrivateRARFile(path)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = file.Write([]byte("swapped"))
		_ = file.Close()
	}
	if err := authority.ReplaceRecord(t.Context(), digest, []byte("new")); !errors.Is(err, errRecordStorageAuthorityInvalid) {
		t.Fatalf("swap = %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "swapped" {
		t.Fatalf("swapped entry = %q, %v", got, err)
	}
	assertCreateNoTemps(t, filepath.Dir(path))
}

func TestRecordStorageAuthorityReplaceRecordIsAtomicAndAuthoritiesDoNotCAS(t *testing.T) {
	lease, err := OpenRepositoryIdentityLease(t.Context(), initSnapshotRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	first, second := createAuthorityForLease(t, lease), createAuthorityForLease(t, lease)
	digest := createDigest("concurrent")
	if err := first.CreateRecord(t.Context(), digest, []byte("old")); err != nil {
		t.Fatal(err)
	}
	start, results := make(chan struct{}), make(chan error, 2)
	reads := make(chan error, 50)
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		<-start
		for range 50 {
			got, readErr := second.ReadRecord(t.Context(), digest, 16)
			if readErr != nil || (string(got) != "old" && string(got) != "first" && string(got) != "second") {
				reads <- errors.New("partial replacement observed")
				return
			}
		}
	}()
	for i, authority := range []*RecordStorageAuthority{first, second} {
		group.Add(1)
		go func(a *RecordStorageAuthority, value []byte) {
			defer group.Done()
			<-start
			results <- a.ReplaceRecord(t.Context(), digest, value)
		}(authority, [][]byte{[]byte("first"), []byte("second")}[i])
	}
	close(start)
	group.Wait()
	close(results)
	close(reads)
	for err := range results {
		if err != nil && !errors.Is(err, errRecordStorageAuthorityInvalid) {
			t.Fatalf("replace = %v", err)
		}
	}
	for err := range reads {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := first.ReadRecord(t.Context(), digest, 16)
	if err != nil || (string(got) != "first" && string(got) != "second") {
		t.Fatalf("final = %q, %v", got, err)
	}
}

func TestRecordStorageAuthorityReplaceRecordCopiesAndCloseWaits(t *testing.T) {
	authority, _ := createAuthority(t)
	digest, original := createDigest("copy"), []byte("original")
	if err := authority.CreateRecord(t.Context(), digest, []byte("old")); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	authority.replaceHook = func() { close(entered); <-release }
	result := make(chan error, 1)
	go func() { result <- authority.ReplaceRecord(t.Context(), digest, original) }()
	<-entered
	copy(original, "changed!")
	closed := make(chan error, 1)
	go func() { closed <- authority.Close() }()
	select {
	case <-closed:
		t.Fatal("Close returned during replacement")
	default:
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	reader := createAuthorityForLease(t, authority.lease)
	got, err := reader.ReadRecord(t.Context(), digest, 16)
	if err != nil || string(got) != "original" {
		t.Fatalf("copied replacement = %q, %v", got, err)
	}
	if err := authority.ReplaceRecord(t.Context(), digest, nil); !errors.Is(err, errRecordStorageAuthorityClosed) {
		t.Fatalf("closed replace = %v", err)
	}
}

func TestPublishWindowsReplaceRelativeReplacesExistingChild(t *testing.T) {
	dir := t.TempDir()
	destination := publishDirectory(t, dir)
	defer windows.CloseHandle(destination)
	if err := os.WriteFile(filepath.Join(dir, "record"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := publishSource(t, filepath.Join(dir, "source"), []byte("new"))
	defer windows.CloseHandle(source)
	record := publishSource(t, filepath.Join(dir, "record"), nil)
	defer windows.CloseHandle(record)
	_, expected, ok := secureWindowsDataInfo(record)
	if !ok {
		t.Fatal("record identity")
	}
	if err := publishWindowsReplaceRelative(source, destination, "record", expected); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "record")); err != nil || string(got) != "new" {
		t.Fatalf("replacement = %q, %v", got, err)
	}
	if err := publishWindowsReplaceRelative(source, destination, "x/y", expected); !errors.Is(err, errWindowsRelativePublishInvalid) {
		t.Fatalf("invalid name = %v", err)
	}
	reparse := filepath.Join(dir, "reparse")
	junctionAuthorityPath(t, reparse)
	other := publishSource(t, filepath.Join(dir, "other"), []byte("other"))
	defer windows.CloseHandle(other)
	if err := publishWindowsReplaceRelative(other, destination, "reparse", expected); !errors.Is(err, errWindowsRelativePublishInvalid) {
		t.Fatalf("reparse destination = %v", err)
	}
	unknown := publishSource(t, filepath.Join(dir, "unknown"), []byte("unknown"))
	defer windows.CloseHandle(unknown)
	_, expected, ok = secureWindowsDataInfo(source)
	if !ok {
		t.Fatal("replacement identity")
	}
	err := publishWindowsRelativeAfter(unknown, destination, "record", true, expected, func() { _ = windows.CloseHandle(destination) })
	if !errors.Is(err, errWindowsRelativePublishUnknown) {
		t.Fatalf("post-rename verification = %v", err)
	}
}
