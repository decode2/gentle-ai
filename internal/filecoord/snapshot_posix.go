//go:build android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package filecoord

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	snapshotDirectoryOpenFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	snapshotFileOpenFlags      = unix.O_RDONLY | unix.O_NONBLOCK | unix.O_NOFOLLOW | unix.O_CLOEXEC
	snapshotReadBufferSize     = 32 << 10
)

var (
	errSnapshotTopology       = errors.New("unsafe snapshot file topology")
	snapshotRead              = func(file *os.File, buffer []byte) (int, error) { return file.Read(buffer) }
	observeSnapshotBackend    = observeSnapshotPOSIX
	revalidateSnapshotBackend = revalidateSnapshotPOSIX
)

type posixSnapshotMetadata struct {
	mode, uid, nlink                         uint64
	dev, ino, size                           uint64
	mtimeSec, mtimeNsec, ctimeSec, ctimeNsec uint64
}

func observeSnapshotPOSIX(ctx context.Context, path string) (*Snapshot, error) {
	snapshot, err := readSnapshotPath(ctx, path)
	return snapshot, snapshotBackendError(err, false)
}

func revalidateSnapshotPOSIX(ctx context.Context, want *Snapshot) error {
	got, err := readSnapshotPath(ctx, want.path)
	if err != nil {
		return snapshotBackendError(err, true)
	}
	return compareSnapshots(*want, *got)
}

func readSnapshotPath(ctx context.Context, path string) (snapshot *Snapshot, err error) {
	file, err := openSnapshotFile(ctx, path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil && err == nil {
			snapshot, err = nil, &OperationalError{Cause: closeErr}
		}
	}()
	return snapshotFromFile(ctx, file, path)
}

func openSnapshotFile(ctx context.Context, path string) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fd, err := unix.Open(string(os.PathSeparator), snapshotDirectoryOpenFlags, 0)
	if err != nil {
		return nil, err
	}
	directory := os.NewFile(uintptr(fd), string(os.PathSeparator))
	defer func() { _ = directory.Close() }()
	components := strings.Split(strings.TrimPrefix(filepath.Clean(path), string(os.PathSeparator)), string(os.PathSeparator))
	if len(components) == 0 || components[len(components)-1] == "" {
		return nil, errSnapshotTopology
	}
	for _, component := range components[:len(components)-1] {
		childFD, err := unix.Openat(int(directory.Fd()), component, snapshotDirectoryOpenFlags, 0)
		if err != nil {
			return nil, err
		}
		_ = directory.Close()
		directory = os.NewFile(uintptr(childFD), component)
	}
	finalFD, err := unix.Openat(int(directory.Fd()), components[len(components)-1], snapshotFileOpenFlags, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(finalFD), filepath.Base(path)), nil
}

func snapshotFromFile(ctx context.Context, file *os.File, path string) (*Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	before, err := statSnapshotFile(file)
	if err != nil {
		return nil, err
	}
	data, err := readSnapshotBytes(ctx, file)
	if err != nil {
		return nil, err
	}
	after, err := statSnapshotFile(file)
	if err != nil {
		if errors.Is(err, errSnapshotTopology) {
			return nil, &ConflictError{Reason: ConflictTopology, Cause: err}
		}
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if conflict := snapshotMetadataConflict(before, after); conflict != nil {
		return nil, conflict
	}
	if uint64(len(data)) != before.size {
		return nil, &ConflictError{Reason: ConflictContent}
	}
	return newSnapshot(path, data, snapshotFileMode(before.mode), uint32(before.mode), before.identity())
}

func statSnapshotFile(file *os.File) (posixSnapshotMetadata, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return posixSnapshotMetadata{}, err
	}
	mode := uint64(stat.Mode)
	if mode&uint64(unix.S_IFMT) != uint64(unix.S_IFREG) || uint64(stat.Nlink) != 1 || uint32(stat.Uid) != uint32(unix.Geteuid()) {
		return posixSnapshotMetadata{}, errSnapshotTopology
	}
	return posixSnapshotMetadata{
		mode: mode, uid: uint64(stat.Uid), nlink: uint64(stat.Nlink),
		dev: uint64(stat.Dev), ino: uint64(stat.Ino), size: uint64(stat.Size),
		mtimeSec: uint64(stat.Mtim.Sec), mtimeNsec: uint64(stat.Mtim.Nsec),
		ctimeSec: uint64(stat.Ctim.Sec), ctimeNsec: uint64(stat.Ctim.Nsec),
	}, nil
}

func (m posixSnapshotMetadata) identity() []byte {
	identity := []byte{1}
	for _, value := range [...]uint64{m.dev, m.ino, m.size, m.mtimeSec, m.mtimeNsec, m.ctimeSec, m.ctimeNsec, m.nlink, m.uid} {
		identity = binary.LittleEndian.AppendUint64(identity, value)
	}
	return identity
}

func snapshotFileMode(mode uint64) fs.FileMode {
	return fs.FileMode((mode & 0o777) | (mode&0o7000)<<11)
}

func snapshotMetadataConflict(before, after posixSnapshotMetadata) error {
	if before.mode != after.mode {
		return &ConflictError{Reason: ConflictMode}
	}
	if before.dev != after.dev || before.ino != after.ino || before.uid != after.uid || before.nlink != after.nlink {
		return &ConflictError{Reason: ConflictIdentity}
	}
	if before.size != after.size || before.mtimeSec != after.mtimeSec || before.mtimeNsec != after.mtimeNsec || before.ctimeSec != after.ctimeSec || before.ctimeNsec != after.ctimeNsec {
		return &ConflictError{Reason: ConflictContent}
	}
	return nil
}

func readSnapshotBytes(ctx context.Context, file *os.File) ([]byte, error) {
	data := make([]byte, 0)
	buffer := make([]byte, snapshotReadBufferSize)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := snapshotRead(file, buffer)
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		if n > 0 {
			data = append(data, buffer[:n]...)
		}
		if err == io.EOF {
			return data, nil
		}
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, io.ErrNoProgress
		}
	}
}

func snapshotBackendError(err error, revalidate bool) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var conflict *ConflictError
	if errors.As(err, &conflict) {
		return err
	}
	if errors.Is(err, unix.ENOENT) {
		if revalidate {
			return &ConflictError{Reason: ConflictMissing, Cause: err}
		}
		return &InvalidTargetError{}
	}
	topology := errors.Is(err, errSnapshotTopology) || errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.EISDIR)
	if topology {
		if revalidate {
			return &ConflictError{Reason: ConflictTopology, Cause: err}
		}
		return &InvalidTargetError{}
	}
	return &OperationalError{Cause: err}
}
