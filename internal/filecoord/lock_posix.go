//go:build android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package filecoord

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const (
	rootDirectoryMode  = 0o700
	directoryOpenFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	lockOpenFlags      = unix.O_RDWR | unix.O_CREAT | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
)

var (
	acquireTimeout       = 30 * time.Second
	acquirePollInterval  = 10 * time.Millisecond
	closeAcquisitionFile = func(file *os.File) error { return file.Close() }
	errUnsafeLockFile    = errors.New("unsafe lock file state")
)

func acquireBackend(ctx context.Context, target, lockRoot string) (*Lease, error) {
	lockPath, err := LockPath(lockRoot, target)
	if err != nil {
		return nil, err
	}
	root, err := openPrivateRoot(filepath.Dir(lockPath))
	if err != nil {
		return nil, err
	}
	file, err := openLockFile(root, filepath.Base(lockPath))
	if err != nil {
		return nil, closeAcquisitionFailure(err, root)
	}
	bounded, cancel := context.WithTimeout(ctx, acquireTimeout)
	defer cancel()
	if err := lockExclusive(bounded, file); err != nil {
		return nil, closeAcquisitionFailure(err, file, root)
	}
	return &Lease{file: file,
		unlock: func(file *os.File) error { return unix.Flock(int(file.Fd()), unix.LOCK_UN) },
		close:  func() error { return errors.Join(file.Close(), root.Close()) },
	}, nil
}

func lockExclusive(ctx context.Context, file *os.File) error {
	for {
		if err := ctx.Err(); err != nil {
			return &BusyError{Cause: err}
		}
		err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EWOULDBLOCK) {
			return &OperationalError{Cause: err}
		}
		select {
		case <-ctx.Done():
			return &BusyError{Cause: ctx.Err()}
		case <-time.After(acquirePollInterval):
		}
	}
}

func openPrivateRoot(path string) (*os.File, error) {
	if filepath.Clean(path) == string(os.PathSeparator) {
		return nil, &InvalidRootError{}
	}
	fd, err := unix.Open(string(os.PathSeparator), directoryOpenFlags, 0)
	if err != nil {
		return nil, &OperationalError{Cause: err}
	}
	directory := os.NewFile(uintptr(fd), string(os.PathSeparator))
	components := strings.Split(strings.TrimPrefix(filepath.Clean(path), string(os.PathSeparator)), string(os.PathSeparator))
	for index, component := range components {
		childFD, err := unix.Openat(int(directory.Fd()), component, directoryOpenFlags, 0)
		if errors.Is(err, unix.ENOENT) {
			mode := uint32(0o755)
			if index == len(components)-1 {
				mode = rootDirectoryMode
			}
			if mkdirErr := unix.Mkdirat(int(directory.Fd()), component, mode); mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
				return nil, closeAcquisitionFailure(rootError(mkdirErr), directory)
			}
			childFD, err = unix.Openat(int(directory.Fd()), component, directoryOpenFlags, 0)
		}
		if err != nil {
			return nil, closeAcquisitionFailure(rootError(err), directory)
		}
		child := os.NewFile(uintptr(childFD), component)
		if index == len(components)-1 {
			if err := verifyPrivateRoot(child); err != nil {
				return nil, closeAcquisitionFailure(err, child, directory)
			}
		}
		if err := closeAcquisitionFile(directory); err != nil {
			return nil, closeAcquisitionFailure(&OperationalError{Cause: err}, child)
		}
		directory = child
	}
	return directory, nil
}

func verifyPrivateRoot(directory *os.File) error {
	var stat unix.Stat_t
	if err := unix.Fstat(int(directory.Fd()), &stat); err != nil {
		return &OperationalError{Cause: err}
	}
	if stat.Mode != unix.S_IFDIR|rootDirectoryMode || stat.Uid != uint32(unix.Geteuid()) {
		return &InvalidRootError{}
	}
	return nil
}

func openLockFile(root *os.File, name string) (*os.File, error) {
	fd, err := unix.Openat(int(root.Fd()), name, lockOpenFlags, 0o600)
	if err != nil {
		return nil, &OperationalError{Cause: err}
	}
	file := os.NewFile(uintptr(fd), name)
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return nil, closeAcquisitionFailure(&OperationalError{Cause: err}, file)
	}
	if stat.Mode != unix.S_IFREG|0o600 || stat.Uid != uint32(unix.Geteuid()) || stat.Nlink != 1 {
		return nil, closeAcquisitionFailure(&OperationalError{Cause: errUnsafeLockFile}, file)
	}
	return file, nil
}

func rootError(err error) error {
	if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
		return &InvalidRootError{}
	}
	return &OperationalError{Cause: err}
}

func closeAcquisitionFailure(primary error, files ...*os.File) error {
	var cleanup error
	for _, file := range files {
		if file != nil {
			cleanup = errors.Join(cleanup, closeAcquisitionFile(file))
		}
	}
	if cleanup != nil {
		return errors.Join(primary, &OperationalError{Cause: cleanup})
	}
	return primary
}
