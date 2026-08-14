//go:build windows

package reviewtransaction

import (
	"context"
	"errors"
	"os"
	"sync"

	"golang.org/x/sys/windows"
)

var (
	errRecordStorageAuthorityInvalid = errors.New("record storage authority is invalid") // refusal:by-design world-action: callers must retain an unchanged lease and hierarchy before opening a new authority
	errRecordStorageAuthorityClosed  = errors.New("record storage authority is closed")  // refusal:by-design world-action: callers must open a new authority after closing the old one
)

const (
	recordStorageGentleAI = "gentle-ai"
	recordStorageRecords  = "direct-run-records"
)

// RecordStorageAuthority retains a verified Windows handle hierarchy for one
// repository's direct-run records. It intentionally exposes no storage access.
type RecordStorageAuthority struct {
	mu               sync.Mutex
	lease            *RepositoryIdentityLease
	storageKey       string
	repositoryDigest string
	commonDir        string
	common           *os.File
	commonID         secureWindowsChildID
	gentleAI         *secureWindowsChild
	records          *secureWindowsChild
	digest           *secureWindowsChild
}

// OpenRecordStorageAuthority opens the Git-common-directory-bound hierarchy.
// The Git directory is shared repository state, so it requires physical,
// current-user-owned validation but not Gentle's owner-only DACL. Every child
// created by Gentle requires that stricter protected owner-only DACL.
func OpenRecordStorageAuthority(ctx context.Context, lease *RepositoryIdentityLease, repositoryDigest string) (*RecordStorageAuthority, error) {
	if lease == nil || !recordStorageDigest(repositoryDigest) {
		return nil, errRecordStorageAuthorityInvalid
	}
	if err := lease.Validate(ctx); err != nil {
		return nil, recordStorageError(ctx)
	}
	identity := lease.Identity()
	a := &RecordStorageAuthority{lease: lease, storageKey: lease.StorageKey(), repositoryDigest: repositoryDigest, commonDir: identity.GitCommonDir}
	if !recordStorageDigest(a.storageKey) || a.commonDir == "" || !recordStorageDigest(repositoryDigest) {
		return nil, errRecordStorageAuthorityInvalid
	}
	common, err := openRARPathNoFollow(a.commonDir, true)
	if err != nil {
		return nil, recordStorageError(ctx)
	}
	a.common = common
	if !a.validCommon() {
		a.Close()
		return nil, errRecordStorageAuthorityInvalid
	}
	var parent windows.Handle = windows.Handle(common.Fd())
	var parentID = a.commonID
	if a.gentleAI, err = recordStorageChild(ctx, parent, &parentID, recordStorageGentleAI); err == nil {
		parent, parentID = a.gentleAI.handle, a.gentleAI.id
	}
	if err == nil {
		a.records, err = recordStorageChild(ctx, parent, &parentID, recordStorageRecords)
		if a.records != nil {
			parent, parentID = a.records.handle, a.records.id
		}
	}
	if err == nil {
		a.digest, err = recordStorageChild(ctx, parent, &parentID, repositoryDigest)
	}
	if err != nil {
		a.Close()
		return nil, recordStorageError(ctx)
	}
	return a, nil
}

func recordStorageChild(ctx context.Context, parent windows.Handle, parentID *secureWindowsChildID, name string) (*secureWindowsChild, error) {
	child, err := createSecureWindowsChildDirectory(ctx, parent, parentID, name)
	if errors.Is(err, errSecureWindowsChildExists) {
		child, err = openSecureWindowsChildDirectory(ctx, parent, parentID, name)
	}
	return child, err
}

func recordStorageDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, b := range value {
		if b >= '0' && b <= '9' || b >= 'a' && b <= 'f' {
			continue
		}
		return false
	}
	return true
}

// Validate proves the retained hierarchy still names the same secure entries.
func (a *RecordStorageAuthority) Validate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a == nil {
		return errRecordStorageAuthorityInvalid
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.common == nil {
		return errRecordStorageAuthorityClosed
	}
	if err := a.lease.Validate(ctx); err != nil || a.storageKey != a.lease.StorageKey() || a.commonDir != a.lease.Identity().GitCommonDir || !a.validCommon() {
		return recordStorageError(ctx)
	}
	parent, parentID := windows.Handle(a.common.Fd()), a.commonID
	for _, entry := range []struct {
		name  string
		child *secureWindowsChild
	}{{recordStorageGentleAI, a.gentleAI}, {recordStorageRecords, a.records}, {a.repositoryDigest, a.digest}} {
		if err := ctx.Err(); err != nil || entry.child == nil || !entry.child.valid() {
			return recordStorageError(ctx)
		}
		opened, err := openSecureWindowsChildDirectory(ctx, parent, &parentID, entry.name)
		if err != nil || opened.id != entry.child.id {
			if opened != nil {
				_ = opened.Close()
			}
			return recordStorageError(ctx)
		}
		_ = opened.Close()
		parent, parentID = entry.child.handle, entry.child.id
	}
	return nil
}

func (a *RecordStorageAuthority) validCommon() bool {
	if a.common == nil {
		return false
	}
	_, id, ok := secureWindowsDirectoryInfo(windows.Handle(a.common.Fd()))
	if !ok {
		return false
	}
	descriptor, err := windows.GetSecurityInfo(windows.Handle(a.common.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil || !rarSecurityDescriptorOwnedByCurrentUser(descriptor) {
		return false
	}
	if a.commonID == (secureWindowsChildID{}) {
		a.commonID = id
	}
	return a.commonID == id
}

// Close releases children deepest-first. It is safe to call repeatedly.
func (a *RecordStorageAuthority) Close() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var first error
	for _, child := range []*secureWindowsChild{a.digest, a.records, a.gentleAI} {
		if child != nil {
			if err := child.Close(); err != nil && first == nil {
				first = err
			}
		}
	}
	if a.common != nil {
		if err := a.common.Close(); err != nil && first == nil {
			first = err
		}
		a.common = nil
	}
	if first != nil {
		return errRecordStorageAuthorityInvalid
	}
	return nil
}

func recordStorageError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errRecordStorageAuthorityInvalid
}
