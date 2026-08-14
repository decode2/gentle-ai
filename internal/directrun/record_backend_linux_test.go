//go:build linux

package directrun

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

func linuxBackend(t *testing.T, repo string) (*linuxRecordBackend, *reviewtransaction.RepositoryIdentityLease) {
	t.Helper()
	if repo == "" {
		repo = t.TempDir()
		if out, err := gitInit(repo); err != nil {
			t.Fatalf("git init: %s: %v", out, err)
		}
	}
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	b, err := newLinuxRecordBackend(t.Context(), lease)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b, lease
}

func TestLinuxRecordBackendStoreLifecycle(t *testing.T) {
	for _, abort := range []bool{false, true} {
		t.Run("terminal", func(t *testing.T) {
			b, lease := linuxBackend(t, "")
			s, err := NewStore(b, lease)
			check(t, err)
			h := testHandoff(t)
			r, err := s.Issue(t.Context(), h)
			check(t, err)
			if abort {
				r, err = s.Abort(t.Context(), h.Identity, r.Revision, AbortCancelled)
			} else {
				r, err = s.Register(t.Context(), h.Identity, r.Revision, "parent-3026", "call-3026", "gentle-worker", "repo-3026", 101, 100)
				check(t, err)
				r, err = s.Bind(t.Context(), h.Identity, r.Revision, "parent-3026", "call-3026", "gentle-worker", "session-3026", "repo-3026", 100)
				check(t, err)
				r, err = s.Consume(t.Context(), h.Identity, r.Revision, "session-3026", "repo-3026")
				check(t, err)
				r, err = s.Finish(t.Context(), h.Identity, r.Revision, OutcomeSucceeded, testOutput(h))
			}
			check(t, err)
			if err := b.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, _ := linuxBackend(t, lease.Identity().RepositoryRoot)
			restore, err := NewStore(reopened, lease)
			check(t, err)
			got, err := restore.Read(t.Context(), h.Identity)
			if err != nil || got.Revision != r.Revision {
				t.Fatalf("reopened = %#v, %v", got, err)
			}
		})
	}
}

func TestLinuxRecordBackendDuplicateAndCAS(t *testing.T) {
	b, lease := linuxBackend(t, "")
	s, err := NewStore(b, lease)
	check(t, err)
	h := testHandoff(t)
	r, err := s.Issue(t.Context(), h)
	check(t, err)
	if again, err := s.Issue(t.Context(), h); err != nil || again.Revision != r.Revision {
		t.Fatalf("duplicate = %#v, %v", again, err)
	}
	different := h
	different.TargetBehavior, different.Revision = "different", ""
	different, err = different.Seal()
	check(t, err)
	if _, err := s.Issue(t.Context(), different); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("conflicting duplicate = %v", err)
	}
	next := bindFor(t, r)
	payload, _ := next.CanonicalJSON()
	if err := b.CompareAndSwap(t.Context(), s.recordKey(h.Identity), "stale", payload); !errors.Is(err, ErrCASConflict) {
		t.Fatalf("stale CAS = %v", err)
	}
	before, _ := b.Read(t.Context(), s.recordKey(h.Identity))
	original, _ := r.CanonicalJSON()
	if !bytes.Equal(before, original) {
		t.Fatal("stale CAS changed bytes")
	}
}

func TestLinuxRecordBackendConcurrentCASAndClose(t *testing.T) {
	b, lease := linuxBackend(t, "")
	s, err := NewStore(b, lease)
	check(t, err)
	h := testHandoff(t)
	r, err := s.Issue(t.Context(), h)
	check(t, err)
	issued := r.Revision
	other, err := newLinuxRecordBackend(t.Context(), lease)
	check(t, err)
	defer other.Close()
	r, _ = r.Register(r.Revision, "parent-3026", "call-3026", "gentle-worker", "repo-3026", 101, 100)
	a, _ := r.Bind(r.Revision, "parent-3026", "call-3026", "gentle-worker", "session-a", "repo-3026", 100)
	c, _ := r.Bind(r.Revision, "parent-3026", "call-3026", "gentle-worker", "session-b", "repo-3026", 100)
	pa, _ := a.CanonicalJSON()
	pc, _ := c.CanonicalJSON()
	start, results := make(chan struct{}), make(chan string, 2)
	for _, call := range []struct {
		winner string
		call   func() error
	}{
		{"a", func() error { return b.CompareAndSwap(t.Context(), s.recordKey(h.Identity), issued, pa) }},
		{"b", func() error { return other.CompareAndSwap(t.Context(), s.recordKey(h.Identity), issued, pc) }},
	} {
		go func(winner string, call func() error) {
			<-start
			if err := call(); err != nil {
				results <- err.Error()
			} else {
				results <- winner
			}
		}(call.winner, call.call)
	}
	close(start)
	winner := ""
	for range 2 {
		if result := <-results; result == "a" || result == "b" {
			if winner != "" {
				t.Fatal("two CAS winners")
			}
			winner = result
		} else if result != ErrCASConflict.Error() {
			t.Fatal(result)
		}
	}
	if winner == "" {
		t.Fatal("no CAS winner")
	}
	got, err := b.Read(t.Context(), s.recordKey(h.Identity))
	want := pa
	if winner == "b" {
		want = pc
	}
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("winner bytes = %q, %v", got, err)
	}
}

func TestLinuxRecordBackendStableFailures(t *testing.T) {
	b, lease := linuxBackend(t, "")
	s, err := NewStore(b, lease)
	check(t, err)
	h := testHandoff(t)
	r, err := s.Issue(t.Context(), h)
	check(t, err)
	key := s.recordKey(h.Identity)
	next := mustRecordBytes(t, bindFor(t, r))
	for _, payload := range [][]byte{[]byte(`{"bad":"payload"}`)} {
		check(t, b.files.Replace(t.Context(), key, payload))
		replaces, replace := 0, b.files.operations.replace
		b.files.operations.replace = func(a int, c, d string) error { replaces++; return replace(a, c, d) }
		before, _ := b.files.Read(t.Context(), key)
		err := b.CompareAndSwap(t.Context(), key, r.Revision, next)
		after, _ := b.files.Read(t.Context(), key)
		if !errors.Is(err, ErrCorruptRecord) || replaces != 0 || !bytes.Equal(before, after) || bytes.Contains([]byte(err.Error()), []byte(lease.Identity().RepositoryRoot)) || bytes.Contains([]byte(err.Error()), payload) {
			t.Fatalf("corrupt CAS = %v", err)
		}
	}
	if _, err := b.Read(t.Context(), RecordKey{Repository: key.Repository, Record: digest("missing", []byte("record"))}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	other, err := newLinuxRecordBackend(t.Context(), lease)
	check(t, err)
	unlock, err := other.lock.Lock(t.Context())
	check(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- b.Create(ctx, RecordKey{key.Repository, digest("new", []byte("record"))}, mustRecordBytes(t, r))
	}()
	closed := make(chan error, 1)
	go func() { closed <- b.Close() }()
	if err := <-result; !errors.Is(err, ErrBackendUnavailable) {
		t.Fatal(err)
	}
	check(t, <-closed)
	check(t, unlock())
	retry, err := newLinuxRecordBackend(t.Context(), lease)
	check(t, err)
	if _, err := retry.Read(t.Context(), RecordKey{key.Repository, digest("new", []byte("record"))}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := retry.Create(t.Context(), RecordKey{key.Repository, digest("new", []byte("record"))}, mustRecordBytes(t, r)); err != nil {
		t.Fatal(err)
	}
	check(t, retry.Close())
	check(t, other.Close())
	if err := b.Close(); err != nil || !b.lock.closed || !b.files.closed {
		t.Fatalf("close = %v", err)
	}
	if _, err := b.Read(t.Context(), key); !errors.Is(err, ErrBackendUnavailable) {
		t.Fatal(err)
	}
}

func TestLinuxRecordBackendReadDuringCASIsComplete(t *testing.T) {
	b, lease := linuxBackend(t, "")
	s, err := NewStore(b, lease)
	check(t, err)
	h := testHandoff(t)
	r, err := s.Issue(t.Context(), h)
	check(t, err)
	next := bindFor(t, r)
	payload, _ := next.CanonicalJSON()
	key := s.recordKey(h.Identity)
	ready, release := make(chan struct{}), make(chan struct{})
	b.files.afterFirstStat = func() {
		b.files.afterFirstStat = nil
		close(ready)
		<-release
	}
	result := make(chan error, 1)
	go func() { result <- b.CompareAndSwap(t.Context(), key, r.Revision, payload) }()
	<-ready
	read := make(chan []byte, 1)
	go func() { value, _ := b.Read(t.Context(), key); read <- value }()
	close(release)
	check(t, <-result)
	if value := <-read; !bytes.Equal(value, payload) && !bytes.Equal(value, mustRecordBytes(t, r)) {
		t.Fatal("read observed partial record")
	}
}

func mustRecordBytes(t *testing.T, record Record) []byte {
	t.Helper()
	value, err := record.CanonicalJSON()
	check(t, err)
	return value
}

func gitInit(repo string) ([]byte, error) { return exec.Command("git", "init", repo).CombinedOutput() }
