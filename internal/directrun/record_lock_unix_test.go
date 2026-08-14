//go:build linux || darwin

package directrun

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestLinuxRecordLockObjectSafety(t *testing.T) {
	for _, test := range []struct {
		name string
		make func(string) error
	}{
		{"symlink", func(p string) error { return os.Symlink(t.TempDir(), p) }}, {"directory", func(p string) error { return os.Mkdir(p, 0o700) }},
		{"fifo", func(p string) error { return unix.Mkfifo(p, 0o600) }}, {"mode", func(p string) error { return os.WriteFile(p, nil, 0o644) }},
		{"special", func(p string) error {
			if err := os.WriteFile(p, nil, 0o600); err != nil {
				return err
			}
			return unix.Chmod(p, 0o4600)
		}},
		{"socket", func(p string) error {
			temp, err := os.CreateTemp("/tmp", "dr-lock-")
			if err != nil {
				return err
			}
			short := temp.Name()
			if err := temp.Close(); err != nil {
				return err
			}
			if err := os.Remove(short); err != nil {
				return err
			}
			fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
			if err != nil {
				return err
			}
			defer unix.Close(fd)
			if err := unix.Bind(fd, &unix.SockaddrUnix{Name: short}); err != nil {
				return err
			}
			return os.Rename(short, p)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, lease := linuxFiles(t)
			p := filepath.Join(recordPaths(lease.Identity().GitCommonDir, f.key)[2], ".record.lock")
			if err := test.make(p); err != nil {
				t.Fatal(err)
			}
			if l, err := newLinuxRecordLock(t.Context(), f); l != nil || !errors.Is(err, ErrBackendUnavailable) {
				t.Fatalf("lock = %v, %v", l, err)
			} else if strings.Contains(err.Error(), lease.Identity().GitCommonDir) {
				t.Fatalf("path leaked: %v", err)
			}
		})
	}
}

func TestLinuxRecordLockCreatedAndTokenSingleUse(t *testing.T) {
	f, lease := linuxFiles(t)
	l, err := newLinuxRecordLock(t.Context(), f)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(recordPaths(lease.Identity().GitCommonDir, f.key)[2], ".record.lock")
	if info, err := os.Stat(p); err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("lock = %v, %v", info, err)
	}
	u, err := l.Lock(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := u(); err != nil {
		t.Fatal(err)
	}
	if err := u(); !errors.Is(err, ErrBackendUnavailable) {
		t.Fatal(err)
	}
}

func TestLinuxRecordLockContentionAndCancel(t *testing.T) {
	f, _ := linuxFiles(t)
	first, err := newLinuxRecordLock(t.Context(), f)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newLinuxRecordLock(t.Context(), f)
	if err != nil {
		t.Fatal(err)
	}
	held, err := first.Lock(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := second.Lock(ctx); !errors.Is(err, ErrBackendUnavailable) {
		t.Fatal(err)
	}
	if err := held(); err != nil {
		t.Fatal(err)
	}
	u, err := second.Lock(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := u(); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxRecordLockRejectsReplacementAndCloses(t *testing.T) {
	f, lease := linuxFiles(t)
	l, err := newLinuxRecordLock(t.Context(), f)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(recordPaths(lease.Identity().GitCommonDir, f.key)[2], ".record.lock")
	if err := os.Rename(p, p+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), p); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Lock(t.Context()); !errors.Is(err, ErrBackendUnavailable) {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Lock(t.Context()); !errors.Is(err, ErrBackendUnavailable) {
		t.Fatal(err)
	}
}

func TestLinuxRecordLockRejectsLeaseAndHierarchyDrift(t *testing.T) {
	f, lease := linuxFiles(t)
	l, err := newLinuxRecordLock(t.Context(), f)
	if err != nil {
		t.Fatal(err)
	}
	common := lease.Identity().GitCommonDir
	if err := os.Rename(common, common+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(common, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Lock(t.Context()); !errors.Is(err, ErrIdentityChanged) {
		t.Fatal(err)
	}
}

func TestLinuxRecordLockCloseRace(t *testing.T) {
	a, _ := linuxFiles(t)
	la, _ := newLinuxRecordLock(t.Context(), a)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			u, e := la.Lock(t.Context())
			if e == nil {
				_ = u()
			}
		}()
	}
	wg.Add(1)
	go func() { defer wg.Done(); _ = la.Close() }()
	wg.Wait()
}

func TestLinuxRecordLockDifferentRepositoriesDoNotContend(t *testing.T) {
	a, first := linuxFiles(t)
	b, second := linuxFiles(t)
	if first.StorageKey() == second.StorageKey() || a.key == b.key {
		t.Fatal("shared lock namespace")
	}
	la, err := newLinuxRecordLock(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	lb, err := newLinuxRecordLock(t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	ua, err := la.Lock(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		u, e := lb.Lock(t.Context())
		if e == nil {
			e = u()
		}
		result <- e
	}()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("different repositories contended")
	}
	if err := ua(); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxRecordLockReleaseOnFDClose(t *testing.T) {
	f, _ := linuxFiles(t)
	first, err := newLinuxRecordLock(t.Context(), f)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newLinuxRecordLock(t.Context(), f)
	if err != nil {
		t.Fatal(err)
	}
	u, err := first.Lock(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Close(first.fd); err != nil {
		t.Fatal(err)
	}
	v, err := second.Lock(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_ = v()
	if err := u(); !errors.Is(err, ErrBackendUnavailable) {
		t.Fatal(err)
	}
	first.closed = true
}
