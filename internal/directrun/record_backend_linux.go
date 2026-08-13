//go:build linux

package directrun

import (
	"context"
	"sync"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

// linuxRecordBackend owns the files and lock for one validated repository lease.
type linuxRecordBackend struct {
	mu     sync.Mutex
	cond   *sync.Cond
	files  *linuxRecordFiles
	lock   *linuxRecordLock
	closed bool
	active int
}

var _ RecordBackend = (*linuxRecordBackend)(nil)

func newLinuxRecordBackend(ctx context.Context, lease *reviewtransaction.RepositoryIdentityLease) (*linuxRecordBackend, error) {
	files, err := newLinuxRecordFiles(ctx, lease)
	if err != nil {
		return nil, err
	}
	lock, err := newLinuxRecordLock(ctx, files)
	if err != nil {
		_ = files.Close()
		return nil, err
	}
	b := &linuxRecordBackend{files: files, lock: lock}
	b.cond = sync.NewCond(&b.mu)
	return b, nil
}

func (b *linuxRecordBackend) begin() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return ErrBackendUnavailable
	}
	b.active++
	return nil
}

func (b *linuxRecordBackend) end() {
	b.mu.Lock()
	b.active--
	if b.active == 0 {
		b.cond.Broadcast()
	}
	b.mu.Unlock()
}

func (b *linuxRecordBackend) Read(ctx context.Context, key RecordKey) ([]byte, error) {
	if err := b.begin(); err != nil {
		return nil, err
	}
	defer b.end()
	return b.files.Read(ctx, key)
}

func (b *linuxRecordBackend) Create(ctx context.Context, key RecordKey, value []byte) error {
	if err := b.begin(); err != nil {
		return err
	}
	defer b.end()
	unlock, err := b.lock.Lock(ctx)
	if err != nil {
		return err
	}
	err = b.files.Create(ctx, key, value)
	if unlock() != nil && err == nil {
		return ErrBackendUnavailable
	}
	return err
}

func (b *linuxRecordBackend) CompareAndSwap(ctx context.Context, key RecordKey, expected Digest, value []byte) error {
	if err := b.begin(); err != nil {
		return err
	}
	defer b.end()
	unlock, err := b.lock.Lock(ctx)
	if err != nil {
		return err
	}
	current, err := b.files.Read(ctx, key)
	if err != nil {
		_ = unlock()
		return err
	}
	record, err := DecodeRecord(current)
	if err != nil {
		_ = unlock()
		return ErrCorruptRecord
	}
	if !recordMatchesKey(record, key, b.files.key) {
		_ = unlock()
		return ErrCorruptRecord
	}
	if record.Revision != expected {
		_ = unlock()
		return ErrCASConflict
	}
	successor, err := DecodeRecord(value)
	if err != nil || !recordMatchesKey(successor, key, b.files.key) {
		_ = unlock()
		return ErrCorruptRecord
	}
	err = b.files.Replace(ctx, key, value)
	if unlock() != nil && err == nil {
		return ErrBackendUnavailable
	}
	return err
}

func recordMatchesKey(record Record, key RecordKey, repository Digest) bool {
	return key.Repository == repository && digest("gentle-ai.direct-run-record-key/v1", []byte(record.Handoff.Identity)) == key.Record
}

func (b *linuxRecordBackend) Close() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	for b.active != 0 {
		b.cond.Wait()
	}
	b.mu.Unlock()
	if err := b.lock.Close(); err != nil {
		return err
	}
	return b.files.Close()
}
