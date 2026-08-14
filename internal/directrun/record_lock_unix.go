//go:build linux || darwin

package directrun

import (
	"context"
	"errors"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// linuxRecordLock orders lock.mu, record-files namespace validation, then flock.
type linuxRecordLock struct {
	mu     sync.Mutex
	files  *linuxRecordFiles
	fd     int
	id     fileIdentity
	closed bool
}

func newLinuxRecordLock(ctx context.Context, files *linuxRecordFiles) (*linuxRecordLock, error) {
	if files == nil {
		return nil, ErrIdentityChanged
	}
	fd, id, err := files.lockFile(ctx, unix.O_RDWR|unix.O_CREAT)
	if err != nil {
		return nil, err
	}
	return &linuxRecordLock{files: files, fd: fd, id: id}, nil
}

// Lock returns a single-use unlock token. The token retains mu so Close cannot reuse its FD.
func (l *linuxRecordLock) Lock(ctx context.Context) (func() error, error) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, ErrBackendUnavailable
	}
	fd, id, err := l.files.lockFile(ctx, unix.O_RDWR)
	if err != nil || id != l.id {
		if fd >= 0 {
			_ = unix.Close(fd)
		}
		l.mu.Unlock()
		return nil, lockError(err)
	}
	if unix.Close(fd) != nil {
		l.mu.Unlock()
		return nil, ErrBackendUnavailable
	}
	for {
		err = unix.Flock(l.fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EINTR) {
			l.mu.Unlock()
			return nil, ErrBackendUnavailable
		}
		select {
		case <-ctx.Done():
			l.mu.Unlock()
			return nil, ErrBackendUnavailable
		case <-time.After(time.Millisecond):
		}
	}
	used := false
	var token sync.Mutex
	return func() error {
		token.Lock()
		defer token.Unlock()
		if used {
			return ErrBackendUnavailable
		}
		used = true
		defer l.mu.Unlock()
		for {
			err := unix.Flock(l.fd, unix.LOCK_UN)
			if !errors.Is(err, unix.EINTR) {
				if err != nil {
					return ErrBackendUnavailable
				}
				return nil
			}
		}
	}, nil
}

func lockError(err error) error {
	if err == nil {
		return ErrBackendUnavailable
	}
	return err
}

func (l *linuxRecordLock) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	if unix.Close(l.fd) != nil {
		return ErrBackendUnavailable
	}
	return nil
}
