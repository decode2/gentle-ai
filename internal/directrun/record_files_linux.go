//go:build linux

package directrun

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
	"golang.org/x/sys/unix"
)

type linuxRecordFiles struct {
	mu             sync.Mutex
	lease          *reviewtransaction.RepositoryIdentityLease
	key            Digest
	root           int
	rootID         fileIdentity
	dirs           [3]fileIdentity
	closed         bool
	afterFirstStat func()
	operations     recordFileOperations
}
type fileIdentity struct{ dev, ino uint64 }

// recordFileOperations is immutable after construction; tests replace it before use.
type recordFileOperations struct {
	writeAll      func(int, []byte) error
	syncFile      func(int) error
	publish       func(int, string, string) error
	syncDirectory func(int) error
	unlinkCleanup func(int, string) error
}

func newRecordFileOperations() recordFileOperations {
	return recordFileOperations{
		writeAll: func(fd int, value []byte) error {
			for len(value) > 0 {
				n, err := unix.Write(fd, value)
				if err != nil || n == 0 {
					return ErrBackendUnavailable
				}
				value = value[n:]
			}
			return nil
		},
		syncFile:      unix.Fsync,
		publish:       func(dir int, tmp, name string) error { return unix.Linkat(dir, tmp, dir, name, 0) },
		syncDirectory: unix.Fsync,
		unlinkCleanup: func(dir int, name string) error { return unix.Unlinkat(dir, name, 0) },
	}
}

func newLinuxRecordFiles(ctx context.Context, lease *reviewtransaction.RepositoryIdentityLease) (*linuxRecordFiles, error) {
	if lease == nil || lease.Validate(ctx) != nil || !hexPart(lease.StorageKey()) {
		return nil, ErrIdentityChanged
	}
	root, err := unix.Open(lease.Identity().GitCommonDir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrBackendUnavailable
	}
	var st unix.Stat_t
	if unix.Fstat(root, &st) != nil {
		_ = unix.Close(root)
		return nil, ErrBackendUnavailable
	}
	f := &linuxRecordFiles{lease: lease, key: digest("gentle-ai.direct-run-store/v1", []byte(lease.StorageKey())), root: root, rootID: fileIdentity{uint64(st.Dev), st.Ino}, operations: newRecordFileOperations()}
	fd, err := f.walk(true)
	if err != nil {
		_ = unix.Close(root)
		return nil, err
	}
	_ = unix.Close(fd)
	return f, nil
}

func (f *linuxRecordFiles) Close() error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	if unix.Close(f.root) != nil {
		return ErrBackendUnavailable
	}
	return nil
}

func (f *linuxRecordFiles) Read(ctx context.Context, key RecordKey) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name, err := f.recordName(ctx, key)
	if err != nil {
		return nil, err
	}
	dir, err := f.walk(false)
	if err != nil {
		return nil, err
	}
	defer unix.Close(dir)
	fd, err := unix.Openat(dir, name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, ErrBackendUnavailable
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || !validRecordStat(&st) {
		return nil, ErrBackendUnavailable
	}
	if st.Size < 0 || st.Size > maxRecordBytes {
		return nil, ErrRecordTooLarge
	}
	if f.afterFirstStat != nil {
		f.afterFirstStat()
	}
	b := make([]byte, int(st.Size))
	for n := 0; n < len(b); {
		k, e := unix.Read(fd, b[n:])
		if e != nil {
			return nil, ErrBackendUnavailable
		}
		if k == 0 {
			return nil, ErrBackendUnavailable
		}
		n += k
	}
	var extra [1]byte
	if n, err := unix.Read(fd, extra[:]); err != nil || n != 0 {
		return nil, ErrBackendUnavailable
	}
	var end unix.Stat_t
	if unix.Fstat(fd, &end) != nil || !sameRecordStat(&st, &end) {
		return nil, ErrBackendUnavailable
	}
	return b, nil
}

func validRecordStat(st *unix.Stat_t) bool {
	return st.Mode&unix.S_IFMT == unix.S_IFREG && st.Mode&0o777 == 0o600
}

func sameRecordStat(a, b *unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Size == b.Size && a.Mode == b.Mode && a.Mtim == b.Mtim && a.Ctim == b.Ctim
}

func (f *linuxRecordFiles) Create(ctx context.Context, key RecordKey, value []byte) (result error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	operations := f.operations
	if len(value) > maxRecordBytes {
		return ErrRecordTooLarge
	}
	name, err := f.recordName(ctx, key)
	if err != nil {
		return err
	}
	dir, err := f.walk(false)
	if err != nil {
		return err
	}
	defer unix.Close(dir)
	if existing, err := unix.Openat(dir, name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0); err == nil {
		_ = unix.Close(existing)
		return ErrAlreadyExists
	} else if !errors.Is(err, unix.ENOENT) {
		return ErrBackendUnavailable
	}
	tmp, fd, err := newRecordTemp(dir)
	if err != nil {
		return ErrBackendUnavailable
	}
	defer unix.Close(fd)
	temporary := true
	defer func() {
		if temporary && operations.unlinkCleanup(dir, tmp) != nil && result == nil {
			result = ErrBackendUnavailable
		}
	}()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0o777 != 0o600 {
		return ErrBackendUnavailable
	}
	if operations.writeAll(fd, value) != nil {
		return ErrBackendUnavailable
	}
	if operations.syncFile(fd) != nil {
		return ErrBackendUnavailable
	}
	if err := operations.publish(dir, tmp, name); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return ErrAlreadyExists
		}
		return ErrBackendUnavailable
	}
	if operations.unlinkCleanup(dir, tmp) != nil {
		_ = operations.unlinkCleanup(dir, name)
		_ = operations.unlinkCleanup(dir, tmp)
		_ = operations.syncDirectory(dir)
		temporary = false
		return ErrBackendUnavailable
	}
	temporary = false
	if operations.syncDirectory(dir) != nil {
		_ = operations.unlinkCleanup(dir, name)
		_ = operations.syncDirectory(dir)
		return ErrBackendUnavailable
	}
	return nil
}

func (f *linuxRecordFiles) recordName(ctx context.Context, key RecordKey) (string, error) {
	if f == nil || f.closed || f.lease.Validate(ctx) != nil || digest("gentle-ai.direct-run-store/v1", []byte(f.lease.StorageKey())) != f.key || key.Repository != f.key || !hexDigest(key.Record) {
		return "", ErrIdentityChanged
	}
	return string(key.Record)[len("sha256:"):], nil
}
func (f *linuxRecordFiles) walk(create bool) (int, error) {
	var root unix.Stat_t
	if unix.Fstat(f.root, &root) != nil || f.rootID != (fileIdentity{uint64(root.Dev), root.Ino}) {
		return -1, ErrBackendUnavailable
	}
	current := f.root
	for i, name := range []string{"gentle-ai", "direct-run-records", string(f.key)[len("sha256:"):]} {
		next, err := unix.Openat(current, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(err, unix.ENOENT) && create {
			if mkdirErr := unix.Mkdirat(current, name, 0o700); mkdirErr == nil || errors.Is(mkdirErr, unix.EEXIST) {
				next, err = unix.Openat(current, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			}
		}
		if current != f.root {
			_ = unix.Close(current)
		}
		if err != nil {
			return -1, ErrBackendUnavailable
		}
		var st unix.Stat_t
		if unix.Fstat(next, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Mode&0o7777 != 0o700 {
			_ = unix.Close(next)
			return -1, ErrBackendUnavailable
		}
		id := fileIdentity{uint64(st.Dev), st.Ino}
		if create {
			f.dirs[i] = id
		} else if f.dirs[i] != id {
			_ = unix.Close(next)
			return -1, ErrBackendUnavailable
		}
		current = next
	}
	return current, nil
}
func newRecordTemp(dir int) (string, int, error) {
	for range 8 {
		var b [12]byte
		if _, err := rand.Read(b[:]); err != nil {
			break
		}
		name := ".record-" + hex.EncodeToString(b[:])
		fd, err := unix.Openat(dir, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if err == nil {
			return name, fd, nil
		}
	}
	return "", -1, ErrBackendUnavailable
}
func hexPart(s string) bool { return len(s) == 64 && strings.Trim(s, "0123456789abcdef") == "" }
func hexDigest(d Digest) bool {
	return strings.HasPrefix(string(d), "sha256:") && hexPart(string(d)[len("sha256:"):])
}
