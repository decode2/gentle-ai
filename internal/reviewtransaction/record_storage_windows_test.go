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
)

func TestRecordStorageAuthorityRejectsInvalidInputsWithoutChildren(t *testing.T) {
	repo := initSnapshotRepo(t)
	lease, err := OpenRepositoryIdentityLease(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, digest := range []string{"", strings.Repeat("A", 64), "sha256:" + strings.Repeat("a", 64), strings.Repeat("a", 63) + ":", `C:\x`, "x:stream"} {
		t.Run(digest, func(t *testing.T) {
			if _, err := OpenRecordStorageAuthority(context.Background(), lease, digest); !errors.Is(err, errRecordStorageAuthorityInvalid) {
				t.Fatalf("error = %v", err)
			}
			if _, statErr := os.Stat(filepath.Join(lease.Identity().GitCommonDir, recordStorageGentleAI)); !os.IsNotExist(statErr) {
				t.Fatalf("invalid digest created hierarchy: %v", statErr)
			}
		})
	}
	if _, err := OpenRecordStorageAuthority(context.Background(), nil, strings.Repeat("a", 64)); !errors.Is(err, errRecordStorageAuthorityInvalid) {
		t.Fatalf("nil lease error = %v", err)
	}
}

func TestRecordStorageAuthorityRetainsPrivateHierarchy(t *testing.T) {
	repo := initSnapshotRepo(t)
	lease, err := OpenRepositoryIdentityLease(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	first, err := OpenRecordStorageAuthority(context.Background(), lease, lease.StorageKey())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := first.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{recordStorageGentleAI, filepath.Join(recordStorageGentleAI, recordStorageRecords), filepath.Join(recordStorageGentleAI, recordStorageRecords, lease.StorageKey())} {
		if err := validatePrivateRARDirectory(filepath.Join(lease.Identity().GitCommonDir, path)); err != nil {
			t.Fatalf("private child %q: %v", path, err)
		}
	}
	second, err := OpenRecordStorageAuthority(context.Background(), lease, lease.StorageKey())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if first.gentleAI.id != second.gentleAI.id || first.records.id != second.records.id || first.digest.id != second.digest.id {
		t.Fatal("reopened hierarchy changed identity")
	}
}

func TestRecordStorageAuthorityDetectsNamespaceReplacement(t *testing.T) {
	repo := initSnapshotRepo(t)
	lease, err := OpenRepositoryIdentityLease(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := OpenRecordStorageAuthority(context.Background(), lease, lease.StorageKey())
	if err != nil {
		t.Fatal(err)
	}
	defer authority.Close()
	path := filepath.Join(lease.Identity().GitCommonDir, recordStorageGentleAI)
	if err := os.Rename(path, path+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := authority.Validate(context.Background()); !errors.Is(err, errRecordStorageAuthorityInvalid) {
		t.Fatalf("replacement error = %v", err)
	}
}

func TestRecordStorageAuthorityCloseIsConcurrentAndRedacted(t *testing.T) {
	repo := initSnapshotRepo(t)
	lease, err := OpenRepositoryIdentityLease(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := OpenRecordStorageAuthority(context.Background(), lease, lease.StorageKey())
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() { defer group.Done(); _ = authority.Close() }()
	}
	group.Wait()
	if err := authority.Validate(context.Background()); !errors.Is(err, errRecordStorageAuthorityClosed) {
		t.Fatalf("post-close error = %v", err)
	}
	secret := `C:\secret\` + strings.Repeat("A", 64)
	_, err = OpenRecordStorageAuthority(context.Background(), lease, secret)
	if !errors.Is(err, errRecordStorageAuthorityInvalid) || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), lease.Identity().GitCommonDir) {
		t.Fatalf("unredacted error = %v", err)
	}
}
