//go:build windows

package reviewtransaction

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	recordLockChildEnvName = "GENTLE_AI_RECORD_LOCK_CHILD"
	recordLockChildPrefix  = "record-storage-lock-child:"
)

// recordLockChild owns every child resource. Its reader reports one protocol
// line, while terminate always unblocks that reader before reaping the process.
type recordLockChild struct {
	cmd   *exec.Cmd
	in    io.WriteCloser
	out   io.ReadCloser
	held  chan error
	done  chan struct{}
	paths []string

	mu       sync.Mutex
	waitOnce sync.Once
	waited   bool
	waitErr  error
}

func TestRecordStorageLockProcessHelper(t *testing.T) {
	if !strings.HasPrefix(os.Getenv(recordLockChildEnvName), recordLockChildPrefix) {
		return
	}
	os.Exit(runRecordStorageLockProcessHelper())
}

func runRecordStorageLockProcessHelper() int {
	repo, digest := os.Getenv("GENTLE_AI_RECORD_LOCK_REPO"), os.Getenv("GENTLE_AI_RECORD_LOCK_DIGEST")
	lease, err := OpenRepositoryIdentityLease(context.Background(), repo)
	if err != nil {
		return 2
	}
	authority, err := OpenRecordStorageAuthority(context.Background(), lease, digest)
	if err != nil {
		return 2
	}
	defer authority.Close()
	lock, err := OpenRecordStorageLock(context.Background(), authority)
	if err != nil {
		return 2
	}
	defer lock.Close()
	release, err := lock.Lock(context.Background())
	if err != nil {
		return 2
	}
	defer release()
	if _, err := io.WriteString(os.Stdout, "HELD\n"); err != nil {
		return 2
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		return 2
	}
	return 0
}

func startRecordLockChild(t *testing.T, repo, digest string) *recordLockChild {
	t.Helper()
	fixture, home, profile, temp := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestRecordStorageLockProcessHelper$")
	cmd.Dir = fixture
	cmd.Env = recordLockChildEnv(t, repo, digest, fixture, home, profile, temp)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal("child stdout setup failed")
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		_ = out.Close()
		t.Fatal("child stdin setup failed")
	}
	if err := cmd.Start(); err != nil {
		_ = in.Close()
		_ = out.Close()
		t.Fatal("child start failed")
	}
	child := &recordLockChild{
		cmd: cmd, in: in, out: out, held: make(chan error, 1), done: make(chan struct{}),
		paths: []string{home, profile, temp},
	}
	t.Cleanup(func() { child.terminate(true) })
	go child.readHeld()
	return child
}

func recordLockChildEnv(t *testing.T, repo, digest, fixture, home, profile, temp string) []string {
	t.Helper()
	env := make([]string, 0, len(os.Environ())+7)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if name != "HOME" && name != "USERPROFILE" && name != "TMP" && name != "TEMP" {
			env = append(env, entry)
		}
	}
	return append(env,
		"HOME="+home,
		"USERPROFILE="+profile,
		"TMP="+temp,
		"TEMP="+temp,
		recordLockChildEnvName+"="+recordLockChildPrefix+fixture,
		"GENTLE_AI_RECORD_LOCK_REPO="+repo,
		"GENTLE_AI_RECORD_LOCK_DIGEST="+digest,
	)
}

func (c *recordLockChild) readHeld() {
	defer close(c.done)
	scanner := bufio.NewScanner(c.out)
	if scanner.Scan() && scanner.Text() == "HELD" {
		c.held <- nil
		return
	}
	c.held <- errors.New("malformed child protocol")
}

func (c *recordLockChild) awaitHeld(t *testing.T) {
	t.Helper()
	select {
	case err := <-c.held:
		if err == nil {
			return
		}
	case <-time.After(10 * time.Second):
	}
	c.terminate(true)
	t.Fatal("child did not prove lease")
}

func (c *recordLockChild) release() error { return c.terminate(false) }

func (c *recordLockChild) crash() error { return c.terminate(true) }

func (c *recordLockChild) terminate(kill bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.in.Close()
	_ = c.out.Close()
	if kill && !c.waited {
		_ = c.cmd.Process.Kill()
	}
	c.waitOnce.Do(func() {
		c.waitErr = c.cmd.Wait()
		c.waited = true
	})
	<-c.done
	return c.waitErr
}

func TestRecordStorageLockAcrossProcesses(t *testing.T) {
	repo := initSnapshotRepo(t)
	lease, err := OpenRepositoryIdentityLease(t.Context(), repo)
	if err != nil {
		t.Fatal("parent lease failed")
	}
	digest := lease.StorageKey()
	child := startRecordLockChild(t, repo, digest)
	child.awaitHeld(t)

	parentAuthority, err := OpenRecordStorageAuthority(t.Context(), lease, digest)
	if err != nil {
		t.Fatal("parent authority failed")
	}
	t.Cleanup(func() { _ = parentAuthority.Close() })
	parent, err := OpenRecordStorageLock(t.Context(), parentAuthority)
	if err != nil {
		t.Fatal("parent lock open failed")
	}
	t.Cleanup(func() { _ = parent.Close() })
	if !validRecordStorageLock(parent.data) || !privateSecureWindowsData(parent.data.handle) {
		t.Fatal("child lock entry is not private regular local data")
	}
	id := parent.data.id

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var cancellationErr error
	_, cancellationErr = parent.Lock(ctx)
	if !errors.Is(cancellationErr, context.Canceled) || cancellationErr != context.Canceled {
		t.Fatal("contended cancellation was not exact")
	}
	if !validRecordStorageLock(parent.data) || parent.data.id != id {
		t.Fatal("cancellation changed child-owned entry")
	}
	assertRecordLockRedacted(t, cancellationErr, append([]string{repo, digest, recordStorageLockName}, child.paths...)...)

	entered, acquired := make(chan struct{}, 1), make(chan error, 1)
	go func() {
		entered <- struct{}{}
		release, err := parent.Lock(t.Context())
		if err == nil {
			err = release()
		}
		acquired <- err
	}()
	<-entered
	if err := child.release(); err != nil {
		t.Fatal("child normal release failed")
	}
	if err := <-acquired; err != nil {
		t.Fatal("parent waiter failed")
	}
	stale, err := OpenRecordStorageLock(t.Context(), parentAuthority)
	if err != nil || stale.data.id != id || !validRecordStorageLock(stale.data) || !privateSecureWindowsData(stale.data.handle) {
		t.Fatal("normal release did not retain the private stale entry")
	}
	if err := stale.Close(); err != nil {
		t.Fatal("stale lock close failed")
	}

	crashed := startRecordLockChild(t, repo, digest)
	crashed.awaitHeld(t)
	if err := crashed.crash(); err == nil {
		t.Fatal("forced child termination unexpectedly succeeded")
	}
	release, err := parent.Lock(t.Context())
	if err != nil {
		t.Fatal("parent did not acquire after crash")
	}
	if err := release(); err != nil {
		t.Fatal("parent release after crash failed")
	}
	afterCrash, err := OpenRecordStorageLock(t.Context(), parentAuthority)
	if err != nil || afterCrash.data.id != id || !validRecordStorageLock(afterCrash.data) || !privateSecureWindowsData(afterCrash.data.handle) {
		t.Fatal("crash changed the private stale entry")
	}
	if err := afterCrash.Close(); err != nil {
		t.Fatal("post-crash lock close failed")
	}

	otherRepo := initSnapshotRepo(t)
	otherLease, err := OpenRepositoryIdentityLease(t.Context(), otherRepo)
	if err != nil {
		t.Fatal("other lease failed")
	}
	otherAuthority, err := OpenRecordStorageAuthority(t.Context(), otherLease, otherLease.StorageKey())
	if err != nil {
		t.Fatal("other authority failed")
	}
	defer otherAuthority.Close()
	other := recordLock(t, otherAuthority)
	defer other.Close()
	holding := startRecordLockChild(t, repo, digest)
	holding.awaitHeld(t)
	otherRelease, err := other.Lock(t.Context())
	if err != nil {
		t.Fatal("separate namespace contended")
	}
	if err := otherRelease(); err != nil {
		t.Fatal("separate namespace release failed")
	}
	if err := holding.crash(); err == nil {
		t.Fatal("final child termination unexpectedly succeeded")
	}
}

func assertRecordLockRedacted(t *testing.T, err error, inputs ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("missing production error")
	}
	for i, input := range inputs {
		if input == "" {
			t.Fatal("missing redaction input")
		}
		for _, other := range inputs[:i] {
			if input == other {
				t.Fatal("redaction inputs are not distinct")
			}
		}
		if strings.Contains(err.Error(), input) {
			t.Fatal("production error exposed storage detail")
		}
	}
}
