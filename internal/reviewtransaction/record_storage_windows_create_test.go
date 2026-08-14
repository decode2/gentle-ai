//go:build windows

package reviewtransaction

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestRecordStorageAuthorityCreateRecordStoresPrivateExactBytes(t *testing.T) {
	for _, test := range []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"small", []byte("record")},
		{"maximum", bytes.Repeat([]byte("x"), 1<<20)},
	} {
		t.Run(test.name, func(t *testing.T) {
			authority, lease := createAuthority(t)
			digest := createDigest(test.name)
			if err := authority.CreateRecord(t.Context(), digest, test.data); err != nil {
				t.Fatal(err)
			}
			got, err := authority.ReadRecord(t.Context(), digest, 1<<20)
			if err != nil || !bytes.Equal(got, test.data) {
				t.Fatalf("ReadRecord() = %q, %v", got, err)
			}
			path := filepath.Join(lease.Identity().GitCommonDir, recordStorageGentleAI, recordStorageRecords, lease.StorageKey(), digest)
			if err := validatePrivateRARFile(path); err != nil {
				t.Fatalf("published file: %v", err)
			}
			data, err := openSecureWindowsChildData(t.Context(), authority.digest.handle, &authority.digest.id, digest)
			if err != nil || !data.valid() {
				t.Fatalf("published handle = %v", err)
			}
			_ = data.Close()
			assertCreateNoTemps(t, filepath.Dir(path))
		})
	}
}

func TestRecordStorageAuthorityCreateRecordRejectsBeforeCreatingTemp(t *testing.T) {
	authority, lease := createAuthority(t)
	dir := filepath.Join(lease.Identity().GitCommonDir, recordStorageGentleAI, recordStorageRecords, lease.StorageKey())
	for _, test := range []struct {
		name   string
		digest string
		data   []byte
		want   error
	}{
		{"invalid", strings.Repeat("A", 64), nil, errRecordStorageAuthorityInvalid},
		{"too-large", createDigest("large"), make([]byte, 1<<20+1), errRecordStorageTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := authority.CreateRecord(t.Context(), test.digest, test.data); !errors.Is(err, test.want) {
				t.Fatalf("CreateRecord() = %v", err)
			} else if strings.Contains(err.Error(), test.digest) || strings.Contains(err.Error(), os.Getenv("HOME")) {
				t.Fatalf("unredacted error: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, test.digest)); !os.IsNotExist(err) {
				t.Fatalf("record side effect: %v", err)
			}
			assertCreateNoTemps(t, dir)
		})
	}
}

func TestRecordStorageAuthorityCreateRecordConflictAndConcurrentWinner(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		t.Run(map[bool]string{false: "conflict", true: "concurrent"}[concurrent], func(t *testing.T) {
			lease, err := OpenRepositoryIdentityLease(t.Context(), initSnapshotRepo(t))
			if err != nil {
				t.Fatal(err)
			}
			first, second := createAuthorityForLease(t, lease), createAuthorityForLease(t, lease)
			digest := createDigest("shared")
			if !concurrent {
				if err := first.CreateRecord(t.Context(), digest, []byte("first")); err != nil {
					t.Fatal(err)
				}
				if err := second.CreateRecord(t.Context(), digest, []byte("second")); !errors.Is(err, errRecordStorageExists) {
					t.Fatalf("conflict = %v", err)
				}
				got, err := first.ReadRecord(t.Context(), digest, 16)
				if err != nil || string(got) != "first" {
					t.Fatalf("winner = %q, %v", got, err)
				}
				assertCreateNoTemps(t, filepath.Join(lease.Identity().GitCommonDir, recordStorageGentleAI, recordStorageRecords, lease.StorageKey()))
				return
			}
			type result struct {
				err  error
				data string
			}
			start, results := make(chan struct{}), make(chan result, 2)
			var group sync.WaitGroup
			for index, authority := range []*RecordStorageAuthority{first, second} {
				group.Add(1)
				go func(a *RecordStorageAuthority, data string) {
					defer group.Done()
					<-start
					results <- result{a.CreateRecord(t.Context(), digest, []byte(data)), data}
				}(authority, []string{"first", "second"}[index])
			}
			close(start)
			group.Wait()
			close(results)
			var successes, exists int
			var winner string
			for result := range results {
				if result.err == nil {
					successes++
					winner = result.data
				}
				if errors.Is(result.err, errRecordStorageExists) {
					exists++
				}
			}
			if successes != 1 || exists != 1 {
				t.Fatalf("results = %d successes, %d conflicts", successes, exists)
			}
			got, err := first.ReadRecord(t.Context(), digest, 16)
			if err != nil || string(got) != winner {
				t.Fatalf("winner = %q, %v", got, err)
			}
		})
	}
}

func TestRecordStorageAuthorityCreateRecordCopiesAndSerializesClose(t *testing.T) {
	authority, _ := createAuthority(t)
	digest, original := createDigest("copy"), []byte("original")
	entered, release := make(chan struct{}), make(chan struct{})
	authority.createHook = func() { close(entered); <-release }
	result := make(chan error, 1)
	go func() { result <- authority.CreateRecord(t.Context(), digest, original) }()
	<-entered
	copy(original, "changed!")
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	got, err := authority.ReadRecord(t.Context(), digest, 16)
	if err != nil || string(got) != "original" {
		t.Fatalf("copied record = %q, %v", got, err)
	}
	authority.storageKey = strings.Repeat("0", 64)
	if err := authority.CreateRecord(t.Context(), createDigest("drift"), nil); !errors.Is(err, errRecordStorageAuthorityInvalid) {
		t.Fatalf("drift create = %v", err)
	}
	authority.storageKey = authority.lease.StorageKey()
	if err := authority.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := authority.ReadRecord(t.Context(), digest, 16); !errors.Is(err, errRecordStorageAuthorityClosed) || got != nil {
		t.Fatalf("post-close = %q, %v", got, err)
	}
	if err := authority.CreateRecord(t.Context(), createDigest("closed"), nil); !errors.Is(err, errRecordStorageAuthorityClosed) {
		t.Fatalf("closed create = %v", err)
	}
}

func createAuthority(t *testing.T) (*RecordStorageAuthority, *RepositoryIdentityLease) {
	t.Helper()
	lease, err := OpenRepositoryIdentityLease(t.Context(), initSnapshotRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	return createAuthorityForLease(t, lease), lease
}

func createAuthorityForLease(t *testing.T, lease *RepositoryIdentityLease) *RecordStorageAuthority {
	t.Helper()
	authority, err := OpenRecordStorageAuthority(t.Context(), lease, lease.StorageKey())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = authority.Close() })
	return authority
}

func createDigest(seed string) string {
	_ = seed
	return strings.Repeat("abcdef", 11)[:64]
}

func assertCreateNoTemps(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "tmp-") {
			t.Fatalf("temp residue: %q", entry.Name())
		}
	}
}
