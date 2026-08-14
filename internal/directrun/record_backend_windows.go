//go:build windows

package directrun

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

type windowsRecordAuthority interface {
	ReadRecord(context.Context, string, int64) ([]byte, error)
	CreateRecord(context.Context, string, []byte) error
	ReplaceRecord(context.Context, string, []byte) error
	Close() error
}

type windowsRecordLock interface {
	Lock(context.Context) (func() error, error)
	Close() error
}

// windowsRecordBackend composes the opaque Windows authority with its retained
// cross-process writer lock. The authority owns all handles and native details.
type windowsRecordBackend struct {
	mu        sync.Mutex
	cond      *sync.Cond
	authority windowsRecordAuthority
	lock      windowsRecordLock
	lease     *reviewtransaction.RepositoryIdentityLease
	key       Digest
	closed    bool
	active    int
}

var _ RecordBackend = (*windowsRecordBackend)(nil)

func newWindowsRecordBackend(ctx context.Context, lease *reviewtransaction.RepositoryIdentityLease) (*windowsRecordBackend, error) {
	return newWindowsRecordBackendWithOpeners(ctx, lease, windowsRecordBackendOpeners{
		authority: func(ctx context.Context, lease *reviewtransaction.RepositoryIdentityLease, storageKey string) (windowsRecordAuthority, error) {
			return reviewtransaction.OpenRecordStorageAuthority(ctx, lease, storageKey)
		},
		lock: func(ctx context.Context, authority windowsRecordAuthority) (windowsRecordLock, error) {
			realAuthority, ok := authority.(*reviewtransaction.RecordStorageAuthority)
			if !ok {
				return nil, ErrBackendUnavailable
			}
			return reviewtransaction.OpenRecordStorageLock(ctx, realAuthority)
		},
	})
}

type windowsRecordBackendOpeners struct {
	authority func(context.Context, *reviewtransaction.RepositoryIdentityLease, string) (windowsRecordAuthority, error)
	lock      func(context.Context, windowsRecordAuthority) (windowsRecordLock, error)
}

func newWindowsRecordBackendWithOpeners(ctx context.Context, lease *reviewtransaction.RepositoryIdentityLease, openers windowsRecordBackendOpeners) (*windowsRecordBackend, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if lease == nil || !windowsStorageKey(lease.StorageKey()) {
		return nil, ErrIdentityChanged
	}
	if err := lease.Validate(ctx); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, ErrIdentityChanged
	}
	key := digest("gentle-ai.direct-run-store/v1", []byte(lease.StorageKey()))
	if openers.authority == nil || openers.lock == nil {
		return nil, ErrBackendUnavailable
	}
	authority, err := openers.authority(ctx, lease, lease.StorageKey())
	if err != nil {
		return nil, mapWindowsStorageError(ctx, err)
	}
	if authority == nil {
		return nil, ErrBackendUnavailable
	}
	lock, err := openers.lock(ctx, authority)
	if err != nil {
		_ = authority.Close()
		return nil, mapWindowsStorageError(ctx, err)
	}
	if lock == nil {
		_ = authority.Close()
		return nil, ErrBackendUnavailable
	}
	return newWindowsRecordBackendFromParts(lease, key, authority, lock), nil
}

// newWindowsRecordBackendFromParts confines test injection to one backend.
func newWindowsRecordBackendFromParts(lease *reviewtransaction.RepositoryIdentityLease, key Digest, authority windowsRecordAuthority, lock windowsRecordLock) *windowsRecordBackend {
	b := &windowsRecordBackend{lease: lease, key: key, authority: authority, lock: lock}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func (b *windowsRecordBackend) begin() error {
	if b == nil {
		return ErrBackendUnavailable
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.cond == nil || b.authority == nil || b.lock == nil {
		return ErrBackendUnavailable
	}
	b.active++
	return nil
}

func (b *windowsRecordBackend) end() {
	if b == nil {
		return
	}
	b.mu.Lock()
	if b.active > 0 {
		b.active--
		if b.active == 0 && b.cond != nil {
			b.cond.Broadcast()
		}
	}
	b.mu.Unlock()
}

func (b *windowsRecordBackend) recordName(ctx context.Context, key RecordKey) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if b == nil || b.lease == nil ||
		digest("gentle-ai.direct-run-store/v1", []byte(b.lease.StorageKey())) != b.key ||
		key.Repository != b.key || !windowsRecordDigest(string(key.Record)) {
		return "", ErrIdentityChanged
	}
	if err := b.lease.Validate(ctx); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", ErrIdentityChanged
	}
	return string(key.Record)[len("sha256:"):], nil
}

func (b *windowsRecordBackend) Read(ctx context.Context, key RecordKey) ([]byte, error) {
	if err := b.begin(); err != nil {
		return nil, err
	}
	defer b.end()
	name, err := b.recordName(ctx, key)
	if err != nil {
		return nil, err
	}
	value, err := b.authority.ReadRecord(ctx, name, maxRecordBytes)
	if err != nil {
		return nil, mapWindowsStorageError(ctx, err)
	}
	return append([]byte(nil), value...), nil
}

func (b *windowsRecordBackend) Create(ctx context.Context, key RecordKey, value []byte) error {
	if err := b.begin(); err != nil {
		return err
	}
	defer b.end()
	name, err := b.recordName(ctx, key)
	if err != nil {
		return err
	}
	unlock, err := b.lock.Lock(ctx)
	if err != nil {
		return mapWindowsStorageError(ctx, err)
	}
	err = b.authority.CreateRecord(ctx, name, value)
	if releaseErr := unlock(); err == nil && releaseErr != nil {
		return ErrBackendUnavailable
	}
	return mapWindowsStorageError(ctx, err)
}

func (b *windowsRecordBackend) CompareAndSwap(ctx context.Context, key RecordKey, expected Digest, value []byte) (result error) {
	if err := b.begin(); err != nil {
		return err
	}
	defer b.end()
	name, err := b.recordName(ctx, key)
	if err != nil {
		return err
	}
	unlock, err := b.lock.Lock(ctx)
	if err != nil {
		return mapWindowsStorageError(ctx, err)
	}
	defer func() {
		if releaseErr := unlock(); result == nil && releaseErr != nil {
			result = ErrBackendUnavailable
		}
	}()
	current, err := b.authority.ReadRecord(ctx, name, maxRecordBytes)
	if err != nil {
		return mapWindowsStorageError(ctx, err)
	}
	record, err := DecodeRecord(current)
	if err != nil || !recordMatchesKey(record, key, b.key) {
		return ErrCorruptRecord
	}
	if record.Revision != expected {
		return ErrCASConflict
	}
	successor, err := DecodeRecord(value)
	if err != nil || !recordMatchesKey(successor, key, b.key) {
		return ErrCorruptRecord
	}
	err = b.authority.ReplaceRecord(ctx, name, value)
	if err != nil {
		return mapWindowsStorageError(ctx, err)
	}
	return nil
}

func (b *windowsRecordBackend) Close() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	for b.active != 0 && b.cond != nil {
		b.cond.Wait()
	}
	lock, authority := b.lock, b.authority
	b.mu.Unlock()
	var first error
	if lock != nil {
		first = lock.Close()
	}
	// Always release the authority even if lock close reports a failure; the lock
	// is no longer usable after Close and retaining the authority would leak it.
	if authority != nil {
		if err := authority.Close(); first == nil {
			first = err
		}
	}
	return mapWindowsStorageError(context.Background(), first)
}

func mapWindowsStorageError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	switch {
	case errors.Is(err, reviewtransaction.ErrRecordStorageMissing):
		return ErrNotFound
	case errors.Is(err, reviewtransaction.ErrRecordStorageExists):
		return ErrAlreadyExists
	case errors.Is(err, reviewtransaction.ErrRecordStorageTooLarge):
		return ErrRecordTooLarge
	default:
		return ErrBackendUnavailable
	}
}

func windowsRecordDigest(value string) bool {
	return len(value) == len("sha256:")+64 && strings.HasPrefix(value, "sha256:") && windowsStorageKey(value[len("sha256:"):])
}

func windowsStorageKey(value string) bool {
	return len(value) == 64 && strings.Trim(value, "0123456789abcdef") == ""
}

func recordMatchesKey(record Record, key RecordKey, repository Digest) bool {
	return key.Repository == repository && digest("gentle-ai.direct-run-record-key/v1", []byte(record.Handoff.Identity)) == key.Record
}
