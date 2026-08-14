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

func TestRecordStorageAuthorityReadRecordBoundsAndContent(t *testing.T) {
	for _, test := range []struct {
		name string
		data []byte
		max  int64
		want error
	}{
		{"missing", nil, 1, errRecordStorageMissing},
		{"empty", []byte{}, 1, nil},
		{"exact", []byte("record"), 6, nil},
		{"exact-max", bytes.Repeat([]byte("x"), 1<<20), 1 << 20, nil},
		{"over-max", []byte("record"), 5, errRecordStorageTooLarge},
		{"zero", []byte("record"), 0, errRecordStorageTooLarge},
		{"negative", []byte("record"), -1, errRecordStorageTooLarge},
		{"hard-cap", []byte("record"), 1<<20 + 1, errRecordStorageTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			authority, path := readAuthority(t, test.data, test.name != "missing")
			got, err := authority.ReadRecord(t.Context(), readDigest, test.max)
			assertReadError(t, err, test.want, path, readDigest)
			if test.want == nil && !bytes.Equal(got, test.data) {
				t.Fatalf("ReadRecord() = %q, want %q", got, test.data)
			}
		})
	}
}

func TestRecordStorageAuthorityReadRecordRejectsInvalidDigestBeforeAccess(t *testing.T) {
	authority, path := readAuthority(t, []byte("record"), true)
	for _, digest := range []string{"", strings.Repeat("A", 64), strings.Repeat("a", 63), `C:\secret`} {
		_, err := authority.ReadRecord(t.Context(), digest, 10)
		assertReadError(t, err, errRecordStorageAuthorityInvalid, path, digest)
	}
}

func TestRecordStorageAuthorityReadRecordDetectsMutation(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{"truncate", func(t *testing.T, path string) {
			t.Helper()
			if err := os.Truncate(path, 1); err != nil {
				t.Fatal(err)
			}
		}},
		{"grow", func(t *testing.T, path string) {
			t.Helper()
			if err := os.Truncate(path, 32); err != nil {
				t.Fatal(err)
			}
		}},
		{"overwrite", func(t *testing.T, path string) {
			t.Helper()
			if err := os.WriteFile(path, []byte("changed!"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"replace", func(t *testing.T, path string) {
			t.Helper()
			if err := os.Rename(path, path+"-old"); err != nil {
				t.Fatal(err)
			}
			file, err := createPrivateRARFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.Write([]byte("replacement")); err != nil {
				t.Fatal(err)
			}
			_ = file.Close()
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			authority, path := readAuthority(t, []byte("original"), true)
			authority.readHook = func() { test.mutate(t, path) }
			got, err := authority.ReadRecord(t.Context(), readDigest, 32)
			if err == nil || len(got) != 0 {
				t.Fatalf("ReadRecord() = %q, %v; want stable failure", got, err)
			}
			assertReadError(t, err, errRecordStorageChanged, path, readDigest)
		})
	}
}

func TestRecordStorageAuthorityReadRecordRevalidatesAndSerializesClose(t *testing.T) {
	authority, path := readAuthority(t, []byte("record"), true)
	authority.storageKey = strings.Repeat("0", 64)
	_, err := authority.ReadRecord(t.Context(), readDigest, 10)
	assertReadError(t, err, errRecordStorageAuthorityInvalid, path, readDigest)
	authority.storageKey = authority.lease.StorageKey()
	authority.readHook = func() { authority.storageKey = strings.Repeat("0", 64) }
	_, err = authority.ReadRecord(t.Context(), readDigest, 10)
	assertReadError(t, err, errRecordStorageAuthorityInvalid, path, readDigest)
	authority.storageKey = authority.lease.StorageKey()
	entered, release := make(chan struct{}), make(chan struct{})
	authority.readHook = func() { close(entered); <-release }
	read := make(chan error, 1)
	go func() { _, err := authority.ReadRecord(context.Background(), readDigest, 10); read <- err }()
	<-entered
	var group sync.WaitGroup
	group.Add(1)
	go func() { defer group.Done(); _ = authority.Close() }()
	close(release)
	if err := <-read; err != nil {
		t.Fatal(err)
	}
	group.Wait()
	_, err = authority.ReadRecord(t.Context(), readDigest, 10)
	assertReadError(t, err, errRecordStorageAuthorityClosed, path, readDigest)
}

func TestRecordStorageAuthorityReadRecordRejectsHostileChild(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{"directory", func(t *testing.T, path string) {
			t.Helper()
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if _, err := createPrivateRARDirectory(path); err != nil {
				t.Fatal(err)
			}
		}},
		{"weak-acl", func(t *testing.T, path string) {
			t.Helper()
			handle := openAuthorityACLHandle(t, path)
			defer windows.CloseHandle(handle)
			weakenAuthorityACL(t, handle)
		}},
		{"reparse", func(t *testing.T, path string) {
			t.Helper()
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			symlinkAuthorityPath(t, path)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			authority, path := readAuthority(t, []byte("record"), true)
			test.setup(t, path)
			_, err := authority.ReadRecord(t.Context(), readDigest, 10)
			assertReadError(t, err, errRecordStorageAuthorityInvalid, path, readDigest)
		})
	}
}

func TestRecordStorageAuthorityReadRecordSeamsAreIsolated(t *testing.T) {
	first, _ := readAuthority(t, []byte("first"), true)
	second, _ := readAuthority(t, []byte("second"), true)
	var firstCalls, secondCalls int
	first.readHook = func() { firstCalls++ }
	second.readHook = func() { secondCalls++ }
	if _, err := first.ReadRecord(t.Context(), readDigest, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := second.ReadRecord(t.Context(), readDigest, 10); err != nil {
		t.Fatal(err)
	}
	if firstCalls != 1 || secondCalls != 1 {
		t.Fatalf("seams = %d, %d", firstCalls, secondCalls)
	}
}

const readDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func readAuthority(t *testing.T, data []byte, create bool) (*RecordStorageAuthority, string) {
	t.Helper()
	lease, err := OpenRepositoryIdentityLease(t.Context(), initSnapshotRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	authority, err := OpenRecordStorageAuthority(t.Context(), lease, lease.StorageKey())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = authority.Close() })
	path := filepath.Join(lease.Identity().GitCommonDir, recordStorageGentleAI, recordStorageRecords, lease.StorageKey(), readDigest)
	if create {
		file, err := createPrivateRARFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(data); err != nil {
			t.Fatal(err)
		}
		_ = file.Close()
	}
	return authority, path
}

func assertReadError(t *testing.T, err, want error, secrets ...string) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	for _, secret := range append(secrets, os.Getenv("HOME"), "S-1-", "0x", "STATUS_") {
		if secret != "" && strings.Contains(err.Error(), secret) {
			t.Fatalf("unredacted error = %v", err)
		}
	}
}
