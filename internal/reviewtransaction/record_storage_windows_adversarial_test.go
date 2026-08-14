//go:build windows

package reviewtransaction

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/windows"
)

func TestRecordStorageAuthorityRejectsHostileChildren(t *testing.T) {
	for _, attack := range []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{"file", writeAuthorityFile},
		{"junction", junctionAuthorityPath},
		{"symlink", symlinkAuthorityPath},
	} {
		for component := range 3 {
			t.Run(attack.name+"-"+string(rune('0'+component)), func(t *testing.T) {
				authority, lease, paths := adversarialAuthority(t)
				if err := authority.Close(); err != nil {
					t.Fatal(err)
				}
				if err := os.RemoveAll(paths[component]); err != nil {
					t.Fatal(err)
				}
				attack.setup(t, paths[component])
				got, err := OpenRecordStorageAuthority(t.Context(), lease, lease.StorageKey())
				if got != nil {
					_ = got.Close()
				}
				assertAuthorityError(t, err, errRecordStorageAuthorityInvalid, lease, lease.StorageKey())
			})
		}
	}
}

func TestRecordStorageAuthorityRejectsWeakACLWithoutRepair(t *testing.T) {
	for component := range 3 {
		t.Run(string(rune('0'+component)), func(t *testing.T) {
			authority, lease, paths := adversarialAuthority(t)
			child := authorityChildren(authority)[component]
			handle := openAuthorityACLHandle(t, paths[component])
			defer windows.CloseHandle(handle)
			weakenAuthorityACL(t, handle)
			if privateSecureWindowsDirectory(child.handle) {
				t.Fatal("weak ACL mutation did not take effect")
			}
			got, err := OpenRecordStorageAuthority(t.Context(), lease, lease.StorageKey())
			if got != nil {
				_ = got.Close()
			}
			assertAuthorityError(t, err, errRecordStorageAuthorityInvalid, lease, lease.StorageKey())
			if privateSecureWindowsDirectory(child.handle) {
				t.Fatal("constructor repaired weak ACL")
			}
		})
	}
}

func TestRecordStorageAuthorityClassifiesReplacementAndDrift(t *testing.T) {
	for component := range 3 {
		t.Run("namespace-"+string(rune('0'+component)), func(t *testing.T) {
			authority, _, paths := adversarialAuthority(t)
			replaceOrPreventAuthorityPath(t, authority, paths[component], authorityIDs(authority)[component], func(t *testing.T, path string) {
				if _, err := createPrivateRARDirectory(path); err != nil {
					t.Fatal(err)
				}
			})
		})
		t.Run("reparse-drift-"+string(rune('0'+component)), func(t *testing.T) {
			authority, _, paths := adversarialAuthority(t)
			replaceOrPreventAuthorityPath(t, authority, paths[component], authorityIDs(authority)[component], junctionAuthorityPath)
		})
		t.Run("acl-drift-"+string(rune('0'+component)), func(t *testing.T) {
			authority, lease, paths := adversarialAuthority(t)
			handle := openAuthorityACLHandle(t, paths[component])
			defer windows.CloseHandle(handle)
			weakenAuthorityACL(t, handle)
			assertAuthorityError(t, authority.Validate(t.Context()), errRecordStorageAuthorityInvalid, lease, lease.StorageKey())
		})
	}
}

func TestRecordStorageAuthorityRejectsForeignOwnerWhenPermitted(t *testing.T) {
	authority, lease, _ := adversarialAuthority(t)
	owner, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	err = windows.SetSecurityInfo(authority.digest.handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, owner, nil, nil, nil)
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_INVALID_OWNER) || errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) {
			t.Skipf("foreign owner mutation unavailable without privilege: %v", err)
		}
		t.Fatal(err)
	}
	assertAuthorityError(t, authority.Validate(t.Context()), errRecordStorageAuthorityInvalid, lease, lease.StorageKey())
}

func TestRecordStorageAuthoritySeparatesLeaseDriftFixtures(t *testing.T) {
	t.Run("common-directory", func(t *testing.T) {
		authority, _, _ := adversarialAuthority(t)
		replaceOrPreventAuthorityPath(t, authority, authority.commonDir, authority.commonID, func(t *testing.T, path string) {
			if _, err := createPrivateRARDirectory(path); err != nil {
				t.Fatal(err)
			}
		})
	})
	t.Run("storage-key", func(t *testing.T) {
		authority, lease, _ := adversarialAuthority(t)
		authority.storageKey = strings.Repeat("0", 64)
		assertAuthorityError(t, authority.Validate(t.Context()), errRecordStorageAuthorityInvalid, lease, lease.StorageKey())
	})
	t.Run("linked-commondir-control", func(t *testing.T) {
		_, linked := initRepositoryIdentityLeaseWorktree(t)
		lease, err := OpenRepositoryIdentityLease(t.Context(), linked)
		if err != nil {
			t.Fatal(err)
		}
		authority, err := OpenRecordStorageAuthority(t.Context(), lease, lease.StorageKey())
		if err != nil {
			t.Fatal(err)
		}
		defer authority.Close()
		controlPath := filepath.Join(lease.Identity().GitDir, "commondir")
		info, err := os.Stat(controlPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(controlPath, []byte(lease.Identity().GitCommonDir+"\n"), info.Mode().Perm()); err != nil {
			t.Fatal(err)
		}
		assertAuthorityError(t, authority.Validate(t.Context()), errRecordStorageAuthorityInvalid, lease, lease.StorageKey())
	})
	t.Run("head-content-remains-mutable", func(t *testing.T) {
		_, linked := initRepositoryIdentityLeaseWorktree(t)
		lease, err := OpenRepositoryIdentityLease(t.Context(), linked)
		if err != nil {
			t.Fatal(err)
		}
		authority, err := OpenRecordStorageAuthority(t.Context(), lease, lease.StorageKey())
		if err != nil {
			t.Fatal(err)
		}
		defer authority.Close()
		headPath := filepath.Join(lease.Identity().GitDir, "HEAD")
		info, err := os.Stat(headPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(headPath, []byte(strings.TrimSpace(gitSnapshot(t, linked, "rev-parse", "HEAD"))+"\n"), info.Mode().Perm()); err != nil {
			t.Fatal(err)
		}
		if err := authority.Validate(t.Context()); err != nil {
			t.Fatalf("valid detached HEAD was rejected: %v", err)
		}
	})
}

func TestRecordStorageAuthorityConcurrentOpenCloseAndIsolation(t *testing.T) {
	repo := initSnapshotRepo(t)
	lease, err := OpenRepositoryIdentityLease(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		authority *RecordStorageAuthority
		err       error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			authority, err := OpenRecordStorageAuthority(t.Context(), lease, lease.StorageKey())
			results <- result{authority, err}
		}()
	}
	close(start)
	firstResult, secondResult := <-results, <-results
	if firstResult.err != nil {
		t.Fatal(firstResult.err)
	}
	if secondResult.err != nil {
		t.Fatal(secondResult.err)
	}
	first, second := firstResult.authority, secondResult.authority
	defer first.Close()
	defer second.Close()
	if authorityIDs(first) != authorityIDs(second) {
		t.Fatal("concurrent opens did not converge")
	}
	other, _, _ := adversarialAuthority(t)
	defer other.Close()
	otherDigest, err := OpenRecordStorageAuthority(t.Context(), lease, strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	defer otherDigest.Close()
	if first.digest.id == other.digest.id || first.digest.id == otherDigest.digest.id {
		t.Fatal("independent repository or digest was not isolated")
	}

	ready, release := make(chan struct{}), make(chan struct{})
	validated := make(chan error, 1)
	go func() { close(ready); <-release; validated <- first.Validate(t.Context()) }()
	<-ready
	var group sync.WaitGroup
	group.Add(2)
	go func() { defer group.Done(); _ = first.Close() }()
	go func() { defer group.Done(); _ = first.Close() }()
	group.Wait()
	close(release)
	assertAuthorityError(t, <-validated, errRecordStorageAuthorityClosed, lease, lease.StorageKey())
	assertAuthorityError(t, first.Validate(t.Context()), errRecordStorageAuthorityClosed, lease, lease.StorageKey())
	if err := second.digest.Close(); err != nil {
		t.Fatal(err)
	}
	assertAuthorityError(t, second.Validate(t.Context()), errRecordStorageAuthorityInvalid, lease, lease.StorageKey())
	second.digest = other.digest // A live unrelated handle must not satisfy the retained child identity.
	assertAuthorityError(t, second.Validate(t.Context()), errRecordStorageAuthorityInvalid, lease, lease.StorageKey())
}

func TestRecordStorageAuthorityPartialConstructionDoesNotAdvance(t *testing.T) {
	for component := range 3 {
		t.Run(string(rune('0'+component)), func(t *testing.T) {
			authority, lease, paths := adversarialAuthority(t)
			if err := authority.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(paths[component]); err != nil {
				t.Fatal(err)
			}
			writeAuthorityFile(t, paths[component])
			got, err := OpenRecordStorageAuthority(t.Context(), lease, lease.StorageKey())
			if got != nil {
				_ = got.Close()
			}
			assertAuthorityError(t, err, errRecordStorageAuthorityInvalid, lease, lease.StorageKey())
			for _, child := range paths[component+1:] {
				if _, err := os.Lstat(child); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("created child after failure: %v", err)
				}
			}
		})
	}
}

func adversarialAuthority(t *testing.T) (*RecordStorageAuthority, *RepositoryIdentityLease, [3]string) {
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
	common := lease.Identity().GitCommonDir
	return authority, lease, [3]string{filepath.Join(common, recordStorageGentleAI), filepath.Join(common, recordStorageGentleAI, recordStorageRecords), filepath.Join(common, recordStorageGentleAI, recordStorageRecords, lease.StorageKey())}
}

func authorityChildren(authority *RecordStorageAuthority) [3]*secureWindowsChild {
	return [3]*secureWindowsChild{authority.gentleAI, authority.records, authority.digest}
}

func authorityIDs(authority *RecordStorageAuthority) [3]secureWindowsChildID {
	children := authorityChildren(authority)
	return [3]secureWindowsChildID{children[0].id, children[1].id, children[2].id}
}

func replaceOrPreventAuthorityPath(t *testing.T, authority *RecordStorageAuthority, path string, want secureWindowsChildID, setup func(*testing.T, string)) {
	t.Helper()
	err := os.Rename(path, path+"-old")
	if err == nil {
		setup(t, path)
		assertAuthorityError(t, authority.Validate(t.Context()), errRecordStorageAuthorityInvalid, authority.lease, authority.repositoryDigest)
		return
	}
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatal(err)
	}
	file, openErr := openRARPathNoFollow(path, true)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer file.Close()
	_, got, ok := secureWindowsDirectoryInfo(windows.Handle(file.Fd()))
	if !ok || got != want {
		t.Fatal("replacement denial did not preserve namespace identity")
	}
	if err := authority.Validate(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Log("replacement prevented by retained handle")
}

func weakenAuthorityACL(t *testing.T, handle windows.Handle) {
	t.Helper()
	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{AccessPermissions: windows.GENERIC_ALL, AccessMode: windows.GRANT_ACCESS, Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_GROUP, TrusteeValue: windows.TrusteeValueFromSID(everyone)}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.UNPROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if descriptor, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION); err != nil || privateRARSecurityDescriptorSafe(descriptor, true) {
		t.Fatalf("weak ACL query = %v", err)
	}
}

func openAuthorityACLHandle(t *testing.T, path string) windows.Handle {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.WRITE_DAC|windows.READ_CONTROL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	return handle
}

func junctionAuthorityPath(t *testing.T, path string) {
	t.Helper()
	output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", path, t.TempDir()).CombinedOutput()
	if err != nil {
		t.Fatalf("native junction: %v: %s", err, output)
	}
}

func symlinkAuthorityPath(t *testing.T, path string) {
	t.Helper()
	if err := os.Symlink(t.TempDir(), path); err != nil {
		if errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) {
			t.Skipf("symlink privilege unavailable: %v", err)
		}
		t.Fatal(err)
	}
}

func writeAuthorityFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertAuthorityError(t *testing.T, err, want error, lease *RepositoryIdentityLease, digest string) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("authority error = %v, want %v", err, want)
	}
	for _, secret := range []string{lease.Identity().RepositoryRoot, lease.Identity().GitCommonDir, digest, os.Getenv("HOME"), "adversarial-secret", "S-1-", "0x", "STATUS_"} {
		if secret != "" && strings.Contains(err.Error(), secret) {
			t.Fatalf("unredacted authority error: %v", err)
		}
	}
}
