//go:build windows

package reviewtransaction

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"sync"

	"golang.org/x/sys/windows"
)

var (
	errRecordStorageAuthorityInvalid   = errors.New("record storage authority is invalid")           // refusal:by-design world-action: callers must retain an unchanged lease and hierarchy before opening a new authority
	errRecordStorageAuthorityClosed    = errors.New("record storage authority is closed")            // refusal:by-design world-action: callers must open a new authority after closing the old one
	errRecordStorageMissing            = errors.New("record storage record is missing")              // refusal:by-design world-action: callers must select an existing published record before reading it
	errRecordStorageTooLarge           = errors.New("record storage record is too large")            // refusal:by-design world-action: callers must request a bounded record size before reading it
	errRecordStorageChanged            = errors.New("record storage record changed")                 // refusal:by-design world-action: callers must retry from a stable published record
	errRecordStorageExists             = errors.New("record storage record already exists")          // refusal:by-design world-action: callers must retain the already-published record rather than overwrite it
	errRecordStorageUnavailable        = errors.New("record storage is unavailable")                 // refusal:by-design world-action: callers must retry only after local secure storage is available
	errRecordStoragePublicationUnknown = errors.New("record storage publication outcome is unknown") // refusal:by-design human-authority: the caller must reconcile the record before treating creation as complete
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
	readHook         func()
	createHook       func()
	replaceHook      func()
	createOps        recordStorageCreateOps
}

// recordStorageCreateOps keeps the Windows CreateRecord boundary injectable per
// authority. Tests configure it before use; CreateRecord reads it while holding
// the authority lock.
type recordStorageCreateOps struct {
	tempName    func() (string, error)
	create      func(context.Context, windows.Handle, *secureWindowsChildID, string) (*secureWindowsData, error)
	write       func(windows.Handle, []byte, *uint32, *windows.Overlapped) error
	flush       func(windows.Handle) error
	verify      func(*secureWindowsData, int) bool
	validate    func(context.Context) error
	publish     func(windows.Handle, windows.Handle, string) error
	postpublish func(context.Context, *secureWindowsData, string, int) bool
	cleanup     func(*secureWindowsData) error
	close       func(*secureWindowsData) error
}

// CreateRecord atomically publishes canonical bytes under their record identity.
// recordDigest identifies the record; it is not a digest of canonical.
func (a *RecordStorageAuthority) CreateRecord(ctx context.Context, recordDigest string, canonical []byte) (result error) {
	const hardMax = 1 << 20
	if a == nil || !recordStorageDigest(recordDigest) {
		return errRecordStorageAuthorityInvalid
	}
	if len(canonical) > hardMax {
		return errRecordStorageTooLarge
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	bytes := append([]byte(nil), canonical...)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.validateLocked(ctx); err != nil {
		return err
	}
	if a.createHook != nil {
		a.createHook()
	}
	ops := a.createOps
	var (
		data *secureWindowsData
		err  error
	)
	for range 8 {
		name, nameErr := ops.tempName()
		if nameErr != nil {
			return errRecordStorageUnavailable
		}
		data, err = ops.create(ctx, a.digest.handle, &a.digest.id, name)
		if !errors.Is(err, errSecureWindowsChildExists) {
			break
		}
	}
	if data == nil {
		return recordStorageCreateError(ctx, err)
	}
	published := false
	defer func() {
		if !published {
			if err := ops.cleanup(data); err != nil {
				result = errRecordStorageUnavailable
			}
			return
		}
		if err := ops.close(data); err != nil && result == nil {
			result = errRecordStoragePublicationUnknown
		}
	}()
	for offset := 0; offset < len(bytes); {
		var wrote uint32
		if err := ops.write(data.handle, bytes[offset:], &wrote, nil); err != nil || wrote == 0 {
			return errRecordStorageUnavailable
		}
		offset += int(wrote)
	}
	if err := ops.flush(data.handle); err != nil || !ops.verify(data, len(bytes)) {
		return errRecordStorageUnavailable
	}
	if err := ops.validate(ctx); err != nil {
		return err
	}
	if err := ops.publish(data.handle, a.digest.handle, recordDigest); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return errRecordStorageExists
		}
		if errors.Is(err, errWindowsRelativePublishUnknown) {
			published = true // The rename may have consumed source; never delete an unknown final.
			return errRecordStoragePublicationUnknown
		}
		return errRecordStorageUnavailable
	}
	published = true
	if !ops.postpublish(ctx, data, recordDigest, len(bytes)) || ops.flush(a.digest.handle) != nil {
		return errRecordStoragePublicationUnknown
	}
	return nil
}

// ReplaceRecord atomically replaces an existing record; it is not CAS.
func (a *RecordStorageAuthority) ReplaceRecord(ctx context.Context, recordDigest string, canonical []byte) (result error) {
	const hardMax = 1 << 20
	if a == nil || !recordStorageDigest(recordDigest) {
		return errRecordStorageAuthorityInvalid
	}
	if len(canonical) > hardMax {
		return errRecordStorageTooLarge
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	bytes := append([]byte(nil), canonical...)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.validateLocked(ctx); err != nil {
		return err
	}
	original, err := openSecureWindowsChildData(ctx, a.digest.handle, &a.digest.id, recordDigest)
	if err != nil {
		return recordStorageReadError(ctx, err)
	}
	originalID := original.id
	if !original.valid() || !localWindowsDiskHandle(original.handle) || original.Close() != nil {
		return errRecordStorageAuthorityInvalid
	}
	ops := a.createOps
	var data *secureWindowsData
	for range 8 {
		name, nameErr := ops.tempName()
		if nameErr != nil {
			return errRecordStorageUnavailable
		}
		data, err = ops.create(ctx, a.digest.handle, &a.digest.id, name)
		if !errors.Is(err, errSecureWindowsChildExists) {
			break
		}
	}
	if data == nil {
		return recordStorageCreateError(ctx, err)
	}
	published := false
	defer func() {
		if !published {
			if err := ops.cleanup(data); err != nil {
				result = errRecordStorageUnavailable
			}
			return
		}
		if err := ops.close(data); err != nil && result == nil {
			result = errRecordStoragePublicationUnknown
		}
	}()
	for offset := 0; offset < len(bytes); {
		var wrote uint32
		if err := ops.write(data.handle, bytes[offset:], &wrote, nil); err != nil || wrote == 0 {
			return errRecordStorageUnavailable
		}
		offset += int(wrote)
	}
	if err := ops.flush(data.handle); err != nil || !ops.verify(data, len(bytes)) {
		return errRecordStorageUnavailable
	}
	if err := ops.validate(ctx); err != nil {
		return err
	}
	current, err := openSecureWindowsChildData(ctx, a.digest.handle, &a.digest.id, recordDigest)
	if err != nil {
		return recordStorageReadError(ctx, err)
	}
	if !current.valid() || !localWindowsDiskHandle(current.handle) || current.id != originalID || current.Close() != nil {
		if current != nil {
			_ = current.Close()
		}
		return errRecordStorageAuthorityInvalid
	}
	if a.replaceHook != nil {
		a.replaceHook()
	}
	if err := publishWindowsReplaceRelative(data.handle, a.digest.handle, recordDigest, originalID); err != nil {
		if errors.Is(err, errWindowsRelativePublishUnknown) {
			published = true
			return errRecordStoragePublicationUnknown
		}
		if errors.Is(err, errSecureWindowsChildMissing) {
			return errRecordStorageMissing
		}
		if errors.Is(err, errWindowsRelativePublishInvalid) || errors.Is(err, errSecureWindowsChildInvalid) {
			return errRecordStorageAuthorityInvalid
		}
		return errRecordStorageUnavailable
	}
	published = true
	if !ops.postpublish(ctx, data, recordDigest, len(bytes)) || ops.flush(a.digest.handle) != nil {
		return errRecordStoragePublicationUnknown
	}
	return nil
}

func recordStorageTempName() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return "tmp-" + hex.EncodeToString(random[:]), nil
}

func recordStorageDataSize(handle windows.Handle, want int) bool {
	info, _, ok := secureWindowsDataInfo(handle)
	return ok && uint64(info.FileSizeHigh)<<32|uint64(info.FileSizeLow) == uint64(want)
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
	a.createOps = a.defaultCreateOps()
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

func (a *RecordStorageAuthority) defaultCreateOps() recordStorageCreateOps {
	return recordStorageCreateOps{
		tempName: recordStorageTempName,
		create:   createSecureWindowsChildData,
		write:    windows.WriteFile,
		flush:    windows.FlushFileBuffers,
		verify: func(data *secureWindowsData, size int) bool {
			return data.valid() && recordStorageDataSize(data.handle, size)
		},
		validate: a.validateLocked,
		publish:  publishWindowsNoReplaceRelative,
		postpublish: func(ctx context.Context, data *secureWindowsData, name string, size int) bool {
			current, err := openSecureWindowsChildData(ctx, a.digest.handle, &a.digest.id, name)
			if err != nil {
				return false
			}
			defer current.Close()
			return current.valid() && current.id == data.id && recordStorageDataSize(current.handle, size)
		},
		cleanup: secureWindowsDeleteData,
		close:   (*secureWindowsData).Close,
	}
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
	return a.validateLocked(ctx)
}

func (a *RecordStorageAuthority) validateLocked(ctx context.Context) error {
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

// ReadRecord returns a stable, bounded snapshot of one record in the retained
// digest directory. It intentionally exposes neither a path nor a handle.
func (a *RecordStorageAuthority) ReadRecord(ctx context.Context, recordDigest string, maxBytes int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a == nil || !recordStorageDigest(recordDigest) {
		return nil, errRecordStorageAuthorityInvalid
	}
	const hardMax = int64(1 << 20)
	if maxBytes <= 0 || maxBytes > hardMax {
		return nil, errRecordStorageTooLarge
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.validateLocked(ctx); err != nil {
		return nil, err
	}
	data, err := openSecureWindowsChildData(ctx, a.digest.handle, &a.digest.id, recordDigest)
	if err != nil {
		return nil, recordStorageReadError(ctx, err)
	}
	defer data.Close()
	first, _, ok := secureWindowsDataInfo(data.handle)
	if !ok || !data.valid() {
		return nil, errRecordStorageAuthorityInvalid
	}
	size := int64(first.FileSizeHigh)<<32 | int64(first.FileSizeLow)
	if size < 0 || size > maxBytes {
		return nil, errRecordStorageTooLarge
	}
	if a.readHook != nil {
		a.readHook()
	}
	bytes := make([]byte, int(size))
	for offset := 0; offset < len(bytes); {
		var read uint32
		if err := windows.ReadFile(data.handle, bytes[offset:], &read, nil); err != nil || read == 0 {
			return nil, errRecordStorageChanged
		}
		offset += int(read)
	}
	var extra uint32
	var probe [1]byte
	if err := windows.ReadFile(data.handle, probe[:], &extra, nil); (err != nil && !errors.Is(err, windows.ERROR_HANDLE_EOF)) || extra != 0 {
		return nil, errRecordStorageChanged
	}
	second, _, ok := secureWindowsDataInfo(data.handle)
	if !ok || !data.valid() || !recordStorageSameInfo(first, second) {
		return nil, errRecordStorageChanged
	}
	if err := a.validateLocked(ctx); err != nil {
		return nil, err
	}
	current, err := openSecureWindowsChildData(ctx, a.digest.handle, &a.digest.id, recordDigest)
	if err != nil {
		return nil, recordStorageReadError(ctx, err)
	}
	defer current.Close()
	if !current.valid() || current.id != data.id {
		return nil, errRecordStorageChanged
	}
	return bytes, nil
}

func recordStorageSameInfo(a, b windows.ByHandleFileInformation) bool {
	return a.VolumeSerialNumber == b.VolumeSerialNumber && a.FileIndexHigh == b.FileIndexHigh && a.FileIndexLow == b.FileIndexLow &&
		a.FileSizeHigh == b.FileSizeHigh && a.FileSizeLow == b.FileSizeLow && a.FileAttributes == b.FileAttributes &&
		a.CreationTime == b.CreationTime && a.LastAccessTime == b.LastAccessTime && a.LastWriteTime == b.LastWriteTime
}

func recordStorageReadError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, errSecureWindowsChildMissing) {
		return errRecordStorageMissing
	}
	return errRecordStorageAuthorityInvalid
}

func recordStorageCreateError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, errSecureWindowsChildExists) {
		return errRecordStorageUnavailable
	}
	return errRecordStorageUnavailable
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
