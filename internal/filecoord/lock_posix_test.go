//go:build android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package filecoord

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func mustAcquire(t *testing.T, target, root string) *Lease {
	t.Helper()
	lease, err := Acquire(context.Background(), target, root)
	if err != nil || lease == nil {
		t.Fatalf("acquire = %v, lease %v", err, lease)
	}
	return lease
}
func mustFS(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func mustMkdir(t *testing.T, path string, mode os.FileMode) string {
	t.Helper()
	mustFS(t, os.Mkdir(path, mode))
	return path
}
func mustWrite(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	mustFS(t, os.WriteFile(path, nil, mode))
}
func mustChmod(t *testing.T, path string, mode os.FileMode) string {
	t.Helper()
	mustFS(t, os.Chmod(path, mode))
	return path
}
func mustSymlink(t *testing.T, target, path string) string {
	t.Helper()
	mustFS(t, os.Symlink(target, path))
	return path
}
func mustHardlink(t *testing.T, old, new string) { t.Helper(); mustFS(t, os.Link(old, new)) }

func TestAcquireLifecycleAndPrivateModes(t *testing.T) {
	root, target := filepath.Join(t.TempDir(), "locks"), filepath.Join(t.TempDir(), "target")
	lease := mustAcquire(t, target, root)
	for _, test := range []struct {
		name, path string
		mode       os.FileMode
	}{{"root", root, 0o700}, {"lock", mustPath(t, root, target), 0o600}} {
		t.Run(test.name, func(t *testing.T) {
			info, err := os.Stat(test.path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != test.mode {
				t.Fatalf("mode = %#o, want %#o", info.Mode().Perm(), test.mode)
			}
		})
	}
	oldTimeout, oldPoll := acquireTimeout, acquirePollInterval
	acquireTimeout, acquirePollInterval = 10*time.Millisecond, time.Millisecond
	defer func() { acquireTimeout, acquirePollInterval = oldTimeout, oldPoll }()
	caller, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	contender, err := Acquire(caller, target, root)
	if contender != nil || !errors.Is(err, ErrBusy) || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("contender = %v, error = %v", contender, err)
	}
	injected, lockName, oldClose := errors.New("descriptor close failed"), filepath.Base(mustPath(t, root, target)), closeAcquisitionFile
	calls := 0
	closeAcquisitionFile = func(file *os.File) error {
		calls++
		err := file.Close()
		if file.Name() == lockName {
			return errors.Join(err, injected)
		}
		return err
	}
	defer func() { closeAcquisitionFile = oldClose }()
	contender, err = Acquire(context.Background(), target, root)
	if contender != nil || !errors.Is(err, ErrBusy) || !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrOperational) || !errors.Is(err, injected) || calls < 2 {
		t.Fatalf("cleanup contender = %v, error = %v, close calls = %d", contender, err, calls)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	reacquired := mustAcquire(t, target, root)
	if err := reacquired.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireRejectsUnsafeRoots(t *testing.T) {
	cases := []struct {
		name string
		root func(*testing.T, string) string
	}{
		{"permissive", func(t *testing.T, b string) string { return mustMkdir(t, filepath.Join(b, "root"), 0o755) }},
		{"non-directory", func(t *testing.T, b string) string { p := filepath.Join(b, "root"); mustWrite(t, p, 0o600); return p }},
		{"intermediate symlink", func(t *testing.T, b string) string {
			return filepath.Join(mustSymlink(t, mustMkdir(t, filepath.Join(b, "real"), 0o700), filepath.Join(b, "link")), "root")
		}},
		{"final symlink", func(t *testing.T, b string) string {
			return mustSymlink(t, mustMkdir(t, filepath.Join(b, "real"), 0o700), filepath.Join(b, "root"))
		}},
		{"special", func(t *testing.T, b string) string {
			return mustChmod(t, mustMkdir(t, filepath.Join(b, "root"), 0o700), 0o700|os.ModeSticky)
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := test.root(t, t.TempDir())
			_, err := Acquire(context.Background(), filepath.Join(t.TempDir(), "target"), root)
			if !errors.Is(err, ErrInvalidRoot) {
				t.Fatalf("error = %v, want ErrInvalidRoot", err)
			}
		})
	}
}

func TestAcquireRejectsUnsafeLockFiles(t *testing.T) {
	write, link := mustWrite, mustHardlink
	cases := []struct {
		name  string
		setup func(*testing.T, string, string)
	}{
		{"permissive", func(t *testing.T, _, lock string) { mustWrite(t, lock, 0o644) }},
		{"owner-extra", func(t *testing.T, _, lock string) { mustWrite(t, lock, 0o700) }},
		{"special", func(t *testing.T, _, lock string) { mustWrite(t, lock, 0o600); mustChmod(t, lock, 0o600|os.ModeSetuid) }},
		{"non-regular", func(t *testing.T, _, lock string) { mustMkdir(t, lock, 0o700) }},
		{"hard-linked", func(t *testing.T, r, l string) { write(t, l, 0o600); link(t, l, filepath.Join(r, "alias")) }},
		{"symlinked", func(t *testing.T, root, lock string) { mustSymlink(t, filepath.Join(root, "other"), lock) }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := mustMkdir(t, filepath.Join(t.TempDir(), "root"), 0o700)
			target := filepath.Join(t.TempDir(), "target")
			lock := mustPath(t, root, target)
			test.setup(t, root, lock)
			_, err := Acquire(context.Background(), target, root)
			if !errors.Is(err, ErrOperational) {
				t.Fatalf("error = %v, want ErrOperational", err)
			}
		})
	}
}

func TestAcquireSubprocessContention(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess contention test")
	}
	root, target := filepath.Join(t.TempDir(), "root"), filepath.Join(t.TempDir(), "target")
	readyR, readyW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	releaseR, releaseW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockHelperProcess$")
	cmd.Env = append(os.Environ(), "FILECOORD_LOCK_HELPER=1", "FILECOORD_LOCK_ROOT="+root, "FILECOORD_LOCK_TARGET="+target)
	cmd.ExtraFiles = []*os.File{readyW, releaseR}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = readyW.Close()
	_ = releaseR.Close()
	defer readyR.Close()
	var signal [1]byte
	if _, err := io.ReadFull(readyR, signal[:]); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	lease, err := Acquire(ctx, target, root)
	if lease != nil || !errors.Is(err, ErrBusy) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("contender = %v, error = %v", lease, err)
	}
	if _, err := releaseW.Write(signal[:]); err != nil {
		t.Fatal(err)
	}
	_ = releaseW.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
}

func helperExit(err error, code int) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(code)
	}
}

func TestLockHelperProcess(t *testing.T) {
	if os.Getenv("FILECOORD_LOCK_HELPER") != "1" {
		return
	}
	ready, release := os.NewFile(3, "ready"), os.NewFile(4, "release")
	lease, err := Acquire(context.Background(), os.Getenv("FILECOORD_LOCK_TARGET"), os.Getenv("FILECOORD_LOCK_ROOT"))
	helperExit(err, 2)
	_, err = ready.Write([]byte{1})
	helperExit(err, 3)
	var signal [1]byte
	_, err = io.ReadFull(release, signal[:])
	helperExit(err, 4)
	helperExit(lease.Release(), 5)
	os.Exit(0)
}
