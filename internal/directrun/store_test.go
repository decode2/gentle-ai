package directrun

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

type memoryLease struct {
	sync.Mutex
	key                                     string
	fail, failAfterRead, changeKeyAfterRead bool
	afterRead                               *memoryBackend
	readAt, storageKeys, validates          int
}

func (l *memoryLease) Validate(context.Context) error {
	l.Lock()
	defer l.Unlock()
	l.validates++
	if l.fail || l.afterRead != nil && l.afterRead.reads > l.readAt && l.failAfterRead {
		return errors.New("/home/me changed")
	}
	if l.afterRead != nil && l.afterRead.reads > l.readAt && l.changeKeyAfterRead {
		l.key = "other-key"
	}
	return nil
}
func (l *memoryLease) StorageKey() string { l.Lock(); defer l.Unlock(); l.storageKeys++; return l.key }

type memoryBackend struct {
	sync.Mutex
	values                 map[RecordKey][]byte
	reads, creates, swaps  int
	err                    error
	readReady, readRelease chan struct{}
}

func (b *memoryBackend) Read(_ context.Context, key RecordKey) ([]byte, error) {
	b.Lock()
	defer b.Unlock()
	b.reads++
	if b.err != nil {
		return nil, b.err
	}
	v, ok := b.values[key]
	if b.readReady != nil {
		b.readReady <- struct{}{}
		b.Unlock()
		<-b.readRelease
		b.Lock()
	}
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), v...), nil
}
func (b *memoryBackend) Create(_ context.Context, key RecordKey, value []byte) error {
	b.Lock()
	defer b.Unlock()
	b.creates++
	if b.err != nil {
		return b.err
	}
	if _, ok := b.values[key]; ok {
		return ErrAlreadyExists
	}
	b.values[key] = append([]byte(nil), value...)
	return nil
}
func (b *memoryBackend) CompareAndSwap(_ context.Context, key RecordKey, expected Digest, value []byte) error {
	b.Lock()
	defer b.Unlock()
	b.swaps++
	if b.err != nil {
		return b.err
	}
	r, err := DecodeRecord(b.values[key])
	if err != nil || r.Revision != expected {
		return ErrCASConflict
	}
	b.values[key] = append([]byte(nil), value...)
	return nil
}
func storeFor(t *testing.T) (*Store, *memoryBackend, *memoryLease) {
	b := &memoryBackend{values: map[RecordKey][]byte{}}
	l := &memoryLease{key: "repository-key"}
	s, err := NewStore(b, l)
	check(t, err)
	return s, b, l
}
func TestStoreLifecycleAndAbort(t *testing.T) {
	for _, abort := range []bool{false, true} {
		t.Run("path", func(t *testing.T) {
			s, _, _ := storeFor(t)
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
			if err != nil || (abort && r.State != RecordAborted) || (!abort && r.State != RecordFinished) {
				t.Fatalf("terminal = %#v, %v", r, err)
			}
			_, err = s.Consume(t.Context(), h.Identity, r.Revision, "session-3026", "repo-3026")
			if !errors.Is(err, ErrReplay) {
				t.Fatalf("replay = %v", err)
			}
		})
	}
}
func TestStoreLeaseAndBounds(t *testing.T) {
	s, b, l := storeFor(t)
	h := testHandoff(t)
	broken := h
	broken.TargetBehavior = "altered"
	keys := l.storageKeys
	if _, err := s.Issue(t.Context(), broken); err == nil || l.validates != 0 || l.storageKeys != keys || b.creates != 0 {
		t.Fatalf("invalid issue = %v validates=%d keys=%d creates=%d", err, l.validates, l.storageKeys, b.creates)
	}
	l.fail = true
	if _, err := s.Read(t.Context(), h.Identity); !errors.Is(err, ErrIdentityChanged) || b.reads != 0 {
		t.Fatalf("drift read = %v calls=%d", err, b.reads)
	}
	l.fail = false
	r, err := s.Issue(t.Context(), h)
	check(t, err)
	l.afterRead, l.readAt, l.failAfterRead = b, b.reads, true
	if _, err := s.Register(t.Context(), h.Identity, r.Revision, "parent-3026", "call-3026", "gentle-worker", "repo-3026", 101, 100); !errors.Is(err, ErrIdentityChanged) || b.reads != 1 || b.swaps != 0 {
		t.Fatalf("pre-CAS lease drift = %v reads=%d swaps=%d", err, b.reads, b.swaps)
	}
	l.key, l.readAt, l.failAfterRead, l.changeKeyAfterRead = "repository-key", b.reads, false, true
	if _, err := s.Register(t.Context(), h.Identity, r.Revision, "parent-3026", "call-3026", "gentle-worker", "repo-3026", 101, 100); !errors.Is(err, ErrIdentityChanged) || b.reads != 2 || b.swaps != 0 {
		t.Fatalf("pre-CAS key drift = %v reads=%d swaps=%d", err, b.reads, b.swaps)
	}
	l.key, l.afterRead, l.changeKeyAfterRead = "repository-key", nil, false
	key := s.recordKey(h.Identity)
	b.values[key] = make([]byte, maxRecordBytes+1)
	if _, err := s.Read(t.Context(), h.Identity); !errors.Is(err, ErrRecordTooLarge) {
		t.Fatalf("oversize read = %v", err)
	}
}
func TestStoreRejectsBadReadsAndStableBackendErrors(t *testing.T) {
	s, b, _ := storeFor(t)
	h := testHandoff(t)
	key := s.recordKey(h.Identity)
	b.values[key] = []byte(`{"schema":"bad"}`)
	if _, err := s.Read(t.Context(), h.Identity); !errors.Is(err, ErrCorruptRecord) {
		t.Fatalf("corrupt = %v", err)
	}
	r, err := IssueRecord(h)
	check(t, err)
	payload, err := r.CanonicalJSON()
	check(t, err)
	b.values[key] = append([]byte("\n"), payload...)
	if _, err := s.Read(t.Context(), h.Identity); !errors.Is(err, ErrCorruptRecord) {
		t.Fatalf("noncanonical = %v", err)
	}
	other, err := NewHandoff("other-run", "worker-3026-example", []string{"/workspace/repo"}, "other behavior", []string{"criterion"}, []Command{{[]string{"go", "test", "./internal/directrun"}, "/workspace/repo"}})
	check(t, err)
	otherRecord, err := IssueRecord(other)
	check(t, err)
	b.values[key], err = otherRecord.CanonicalJSON()
	check(t, err)
	if _, err := s.Read(t.Context(), h.Identity); !errors.Is(err, ErrNotFound) {
		t.Fatalf("identity mismatch = %v", err)
	}
	b.err = errors.New("/home/me/.secret: backend exploded")
	if _, err := s.Read(t.Context(), h.Identity); !errors.Is(err, ErrBackendUnavailable) || strings.Contains(err.Error(), "/home") {
		t.Fatalf("backend error leaked = %v", err)
	}
}
func TestStoreIssueDuplicateAndBoundedSuccessor(t *testing.T) {
	s, b, _ := storeFor(t)
	h := testHandoff(t)
	oversized := h
	oversized.TargetBehavior = strings.Repeat("x", maxRecordBytes)
	oversized.Revision = ""
	oversized, err := oversized.Seal()
	check(t, err)
	if _, err := s.Issue(t.Context(), oversized); !errors.Is(err, ErrRecordTooLarge) || b.creates != 0 {
		t.Fatalf("oversize issue = %v creates=%d", err, b.creates)
	}
	first, err := s.Issue(t.Context(), h)
	check(t, err)
	again, err := s.Issue(t.Context(), h)
	if err != nil || again.Revision != first.Revision {
		t.Fatalf("duplicate = %#v, %v", again, err)
	}
	different := h
	different.TargetBehavior = "different behavior"
	different.Revision = ""
	different, err = different.Seal()
	check(t, err)
	if _, err := s.Issue(t.Context(), different); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("different issue = %v", err)
	}
	registered, err := s.Register(t.Context(), h.Identity, first.Revision, "parent-3026", "call-3026", "gentle-worker", "repo-3026", 101, 100)
	check(t, err)
	bound, err := s.Bind(t.Context(), h.Identity, registered.Revision, "parent-3026", "call-3026", "gentle-worker", "session-3026", "repo-3026", 100)
	check(t, err)
	consumed, err := s.Consume(t.Context(), h.Identity, bound.Revision, "session-3026", "repo-3026")
	check(t, err)
	output := testOutput(h)
	output.Summary = strings.Repeat("x", maxRecordBytes)
	if _, err := s.Finish(t.Context(), h.Identity, consumed.Revision, OutcomeSucceeded, output); !errors.Is(err, ErrRecordTooLarge) || b.swaps != 3 {
		t.Fatalf("oversize successor = %v swaps=%d", err, b.swaps)
	}
}
func TestStoreCASConflictHasNoRetry(t *testing.T) {
	s, b, _ := storeFor(t)
	h := testHandoff(t)
	r, err := s.Issue(t.Context(), h)
	check(t, err)
	start := make(chan struct{})
	errs := make(chan error, 2)
	b.readReady, b.readRelease = make(chan struct{}, 2), make(chan struct{})
	for range 2 {
		go func() {
			<-start
			_, err := s.Register(context.Background(), h.Identity, r.Revision, "parent-3026", "call-3026", "gentle-worker", "repo-3026", 101, 100)
			errs <- err
		}()
	}
	close(start)
	<-b.readReady
	<-b.readReady
	close(b.readRelease)
	var won, lost int
	for range 2 {
		if err := <-errs; err == nil {
			won++
		} else if errors.Is(err, ErrCASConflict) {
			lost++
		} else {
			t.Fatal(err)
		}
	}
	if won != 1 || lost != 1 || b.swaps != 2 {
		t.Fatalf("CAS outcomes won=%d lost=%d swaps=%d", won, lost, b.swaps)
	}
}
