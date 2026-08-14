//go:build windows

package directrun

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

type fakeWindowsAuthority struct {
	mu          sync.Mutex
	values      map[string][]byte
	readErr     error
	createErr   error
	replaceErr  error
	closeErr    error
	closed      int
	reads       int
	creates     int
	replaces    int
	readStarted chan struct{}
	readRelease chan struct{}
	trace       *[]string
}

func (a *fakeWindowsAuthority) ReadRecord(_ context.Context, name string, _ int64) ([]byte, error) {
	if a.readStarted != nil {
		close(a.readStarted)
		<-a.readRelease
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reads++
	if a.readErr != nil {
		return nil, a.readErr
	}
	v, ok := a.values[name]
	if !ok {
		return nil, reviewtransaction.ErrRecordStorageMissing
	}
	return append([]byte(nil), v...), nil
}
func (a *fakeWindowsAuthority) CreateRecord(_ context.Context, name string, value []byte) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.creates++
	if a.createErr != nil {
		return a.createErr
	}
	if _, ok := a.values[name]; ok {
		return reviewtransaction.ErrRecordStorageExists
	}
	a.values[name] = append([]byte(nil), value...)
	return nil
}
func (a *fakeWindowsAuthority) ReplaceRecord(_ context.Context, name string, value []byte) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.replaces++
	if a.replaceErr != nil {
		return a.replaceErr
	}
	if _, ok := a.values[name]; !ok {
		return reviewtransaction.ErrRecordStorageMissing
	}
	a.values[name] = append([]byte(nil), value...)
	return nil
}
func (a *fakeWindowsAuthority) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed++
	if a.trace != nil {
		*a.trace = append(*a.trace, "authority.close")
	}
	return a.closeErr
}

type fakeWindowsLock struct {
	mu                      sync.Mutex
	releaseErr, closeErr    error
	locks, releases, closes int
	trace                   *[]string
}

func (l *fakeWindowsLock) Lock(ctx context.Context) (func() error, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	l.locks++
	used := false
	return func() error {
		if used {
			return ErrBackendUnavailable
		}
		used = true
		l.releases++
		l.mu.Unlock()
		return l.releaseErr
	}, nil
}
func (l *fakeWindowsLock) Close() error {
	l.closes++
	if l.trace != nil {
		*l.trace = append(*l.trace, "lock.close")
	}
	return l.closeErr
}

func windowsBackend(t *testing.T) (*windowsRecordBackend, *fakeWindowsAuthority, *fakeWindowsLock, RecordKey, Record) {
	t.Helper()
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	key := Digest("sha256:") + Digest(lease.StorageKey())
	authority := &fakeWindowsAuthority{values: map[string][]byte{}}
	lock := &fakeWindowsLock{}
	b := newWindowsRecordBackendFromParts(lease, digest("gentle-ai.direct-run-store/v1", []byte(lease.StorageKey())), authority, lock)
	r, err := IssueRecord(testHandoff(t))
	if err != nil {
		t.Fatal(err)
	}
	return b, authority, lock, RecordKey{Repository: b.key, Record: key}, r
}

func TestWindowsRecordBackendMapsStorageAndValidatesBeforeLock(t *testing.T) {
	b, authority, lock, key, record := windowsBackend(t)
	payload, _ := record.CanonicalJSON()
	if err := b.Create(t.Context(), key, payload); err != nil {
		t.Fatal(err)
	}
	if err := b.Create(t.Context(), key, payload); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate = %v", err)
	}
	if _, err := b.Read(t.Context(), RecordKey{}); !errors.Is(err, ErrIdentityChanged) || lock.locks != 2 {
		t.Fatalf("invalid key = %v, locks = %d", err, lock.locks)
	}
	authority.readErr = errors.New("C:\\Users\\secret\\HOME sha256:deadbeef status=0xc0000001")
	if _, err := b.Read(t.Context(), key); err != ErrBackendUnavailable {
		t.Fatalf("redaction = %v", err)
	}
	if _, err := b.Read(context.WithValue(t.Context(), "unused", "unused"), key); err != ErrBackendUnavailable {
		t.Fatal(err)
	}
	lock.releaseErr = errors.New("release failure")
	other := key
	other.Record = Digest("sha256:") + Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err := b.Create(t.Context(), other, payload); err != ErrBackendUnavailable {
		t.Fatalf("create release = %v", err)
	}
}

func TestMapWindowsStorageError(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want error
	}{
		{"missing", reviewtransaction.ErrRecordStorageMissing, ErrNotFound},
		{"exists", reviewtransaction.ErrRecordStorageExists, ErrAlreadyExists},
		{"too large", reviewtransaction.ErrRecordStorageTooLarge, ErrRecordTooLarge},
		{"private detail", errors.New("C:\\Users\\root HOME sha256:deadbeef status=0xc0000001"), ErrBackendUnavailable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := mapWindowsStorageError(t.Context(), tt.err); got != tt.want {
				t.Fatalf("map = %v", got)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := mapWindowsStorageError(ctx, reviewtransaction.ErrRecordStorageMissing); !errors.Is(err, context.Canceled) {
		t.Fatalf("context = %v", err)
	}
}

func TestWindowsRecordBackendCASAndReleasePrecedence(t *testing.T) {
	b, authority, lock, key, record := windowsBackend(t)
	current, _ := record.CanonicalJSON()
	authority.values[string(key.Record)[len("sha256:"):]] = current
	next := bindFor(t, record)
	successor, _ := next.CanonicalJSON()
	if err := b.CompareAndSwap(t.Context(), key, "stale", successor); !errors.Is(err, ErrCASConflict) {
		t.Fatalf("stale = %v", err)
	}
	if got := authority.values[string(key.Record)[len("sha256:"):]]; !bytes.Equal(got, current) {
		t.Fatal("stale CAS changed value")
	}
	if err := b.CompareAndSwap(t.Context(), key, record.Revision, []byte(`{"bad":true}`)); !errors.Is(err, ErrCorruptRecord) {
		t.Fatalf("corrupt successor = %v", err)
	}
	if err := b.CompareAndSwap(t.Context(), key, record.Revision, successor); err != nil {
		t.Fatal(err)
	}
	lock.releaseErr = errors.New("release failed")
	if err := b.CompareAndSwap(t.Context(), key, next.Revision, current); !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("release = %v", err)
	}
	if lock.releases != lock.locks {
		t.Fatalf("releases = %d, locks = %d", lock.releases, lock.locks)
	}
}

func TestWindowsRecordBackendCloseClosesBothInOrder(t *testing.T) {
	b, authority, lock, _, _ := windowsBackend(t)
	lock.closeErr = errors.New("native status 0xc0000001")
	if err := b.Close(); err != ErrBackendUnavailable {
		t.Fatalf("close = %v", err)
	}
	if authority.closed != 1 || lock.closes != 1 {
		t.Fatalf("authority closes = %d, lock closes = %d", authority.closed, lock.closes)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Read(t.Context(), RecordKey{}); err != ErrBackendUnavailable {
		t.Fatalf("closed read = %v", err)
	}
}

func TestWindowsRecordBackendNilAndContext(t *testing.T) {
	var b *windowsRecordBackend
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Read(t.Context(), RecordKey{}); err != ErrBackendUnavailable {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := newWindowsRecordBackend(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("constructor = %v", err)
	}
	var zero windowsRecordBackend
	if _, err := zero.Read(t.Context(), RecordKey{}); err != ErrBackendUnavailable {
		t.Fatalf("zero read = %v", err)
	}
	if err := zero.Close(); err != nil {
		t.Fatalf("zero close = %v", err)
	}
}

func TestWindowsRecordBackendConstructorOpeners(t *testing.T) {
	prototype, _, _, _, _ := windowsBackend(t)
	lease := prototype.lease
	poison := errors.New(`C:\Users\root HOME sha256:deadbeef status=3221225473/0xc0000001`)
	for _, tt := range []struct {
		name          string
		authorityErr  error
		lockErr       error
		want          error
		wantAuthority int
		wantLock      int
		close         bool
	}{
		{"authority failure", poison, nil, ErrBackendUnavailable, 0, 0, false},
		{"lock failure cleans authority", nil, poison, ErrBackendUnavailable, 1, 1, false},
		{"successful ownership", nil, nil, nil, 0, 1, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			authority := &fakeWindowsAuthority{values: map[string][]byte{}}
			lock := &fakeWindowsLock{}
			lockCalls := 0
			backend, err := newWindowsRecordBackendWithOpeners(t.Context(), lease, windowsRecordBackendOpeners{
				authority: func(context.Context, *reviewtransaction.RepositoryIdentityLease, string) (windowsRecordAuthority, error) {
					return authority, tt.authorityErr
				},
				lock: func(context.Context, windowsRecordAuthority) (windowsRecordLock, error) {
					lockCalls++
					return lock, tt.lockErr
				},
			})
			if err != tt.want {
				t.Fatalf("constructor = %v", err)
			}
			if authority.closed != tt.wantAuthority || lockCalls != tt.wantLock {
				t.Fatalf("authority closes = %d, lock calls = %d", authority.closed, lockCalls)
			}
			if tt.close {
				if err := backend.Close(); err != nil || authority.closed != 1 || lock.closes != 1 {
					t.Fatalf("close = %v, authority = %d, lock = %d", err, authority.closed, lock.closes)
				}
			}
		})
	}
}

func TestWindowsRecordBackendCloseWaitsAndClosesInOrder(t *testing.T) {
	b, authority, lock, key, record := windowsBackend(t)
	payload, _ := record.CanonicalJSON()
	authority.values[string(key.Record)[len("sha256:"):]] = payload
	authority.readStarted, authority.readRelease = make(chan struct{}), make(chan struct{})
	trace := []string{}
	authority.trace, lock.trace = &trace, &trace
	read := make(chan error, 1)
	go func() { _, err := b.Read(t.Context(), key); read <- err }()
	<-authority.readStarted
	closed := make(chan error, 1)
	go func() { closed <- b.Close() }()
	if err := b.Create(t.Context(), key, payload); err != ErrBackendUnavailable {
		t.Fatalf("work admitted while closing = %v", err)
	}
	select {
	case err := <-closed:
		t.Fatalf("close completed before active read: %v", err)
	default:
	}
	if len(trace) != 0 {
		t.Fatalf("dependencies closed before active read ended: %v", trace)
	}
	close(authority.readRelease)
	if err := <-read; err != nil {
		t.Fatal(err)
	}
	if err := <-closed; err != nil || len(trace) != 2 || trace[0] != "lock.close" || trace[1] != "authority.close" {
		t.Fatalf("close = %v, trace = %v", err, trace)
	}
	if err := b.Close(); err != nil || len(trace) != 2 {
		t.Fatalf("repeat close = %v, trace = %v", err, trace)
	}
}

func TestWindowsRecordBackendConstructorCleanupPreservesContext(t *testing.T) {
	prototype, _, _, _, _ := windowsBackend(t)
	ctx, cancel := context.WithCancel(t.Context())
	authority := &fakeWindowsAuthority{values: map[string][]byte{}, closeErr: errors.New("HOME root status=9/0x9")}
	_, err := newWindowsRecordBackendWithOpeners(ctx, prototype.lease, windowsRecordBackendOpeners{
		authority: func(context.Context, *reviewtransaction.RepositoryIdentityLease, string) (windowsRecordAuthority, error) {
			return authority, nil
		},
		lock: func(context.Context, windowsRecordAuthority) (windowsRecordLock, error) {
			cancel()
			return nil, errors.New("HOME root status=10/0xa")
		},
	})
	if !errors.Is(err, context.Canceled) || authority.closed != 1 {
		t.Fatalf("constructor = %v, authority closes = %d", err, authority.closed)
	}
}

func TestWindowsRecordBackendCloseFailurePrecedence(t *testing.T) {
	for _, tt := range []struct {
		name         string
		lockErr      error
		authorityErr error
	}{
		{"authority", nil, errors.New("HOME C:\\root status=5/0x5")},
		{"lock", errors.New("HOME C:\\root status=6/0x6"), nil},
		{"both lock precedence", errors.New("lock 7/0x7"), errors.New("authority 8/0x8")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b, authority, lock, _, _ := windowsBackend(t)
			trace := []string{}
			authority.trace, lock.trace = &trace, &trace
			lock.closeErr, authority.closeErr = tt.lockErr, tt.authorityErr
			if err := b.Close(); err != ErrBackendUnavailable {
				t.Fatalf("close = %v", err)
			}
			if got := strings.Join(trace, ","); got != "lock.close,authority.close" || lock.closes != 1 || authority.closed != 1 {
				t.Fatalf("trace = %q, closes = %d/%d", got, lock.closes, authority.closed)
			}
		})
	}
}

func TestWindowsRecordBackendKeyValidationAndLeaseDrift(t *testing.T) {
	b, authority, lock, key, _ := windowsBackend(t)
	for _, tt := range []struct {
		name string
		key  RecordKey
		ctx  context.Context
		want error
	}{
		{"repository mismatch", RecordKey{Repository: digest("other", nil), Record: key.Record}, t.Context(), ErrIdentityChanged},
		{"uppercase", RecordKey{Repository: key.Repository, Record: Digest("sha256:") + Digest("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")}, t.Context(), ErrIdentityChanged},
		{"nonhex", RecordKey{Repository: key.Repository, Record: Digest("sha256:") + Digest("gggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggg")}, t.Context(), ErrIdentityChanged},
		{"short", RecordKey{Repository: key.Repository, Record: "sha256:abc"}, t.Context(), ErrIdentityChanged},
		{"long", RecordKey{Repository: key.Repository, Record: Digest("sha256:") + Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")}, t.Context(), ErrIdentityChanged},
		{"empty", RecordKey{Repository: key.Repository}, t.Context(), ErrIdentityChanged},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reads, creates, replaces, locks := authority.reads, authority.creates, authority.replaces, lock.locks
			if _, err := b.Read(tt.ctx, tt.key); err != tt.want || authority.reads != reads || authority.creates != creates || authority.replaces != replaces || lock.locks != locks {
				t.Fatalf("read = %v, calls = %d/%d/%d, locks = %d", err, authority.reads, authority.creates, authority.replaces, lock.locks)
			}
			if err := b.Create(tt.ctx, tt.key, nil); err != tt.want || authority.reads != reads || authority.creates != creates || authority.replaces != replaces || lock.locks != locks {
				t.Fatalf("create = %v, calls = %d/%d/%d, locks = %d", err, authority.reads, authority.creates, authority.replaces, lock.locks)
			}
			if err := b.CompareAndSwap(tt.ctx, tt.key, "", nil); err != tt.want || authority.reads != reads || authority.creates != creates || authority.replaces != replaces || lock.locks != locks {
				t.Fatalf("cas = %v, calls = %d/%d/%d, locks = %d", err, authority.reads, authority.creates, authority.replaces, lock.locks)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := b.Read(ctx, key); !errors.Is(err, context.Canceled) || lock.locks != 0 {
		t.Fatalf("cancelled read = %v", err)
	}
	if _, err := b.Read(t.Context(), key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("valid key = %v", err)
	}
	gitDir := filepath.Join(b.lease.Identity().RepositoryRoot, ".git")
	driftDir := filepath.Join(b.lease.Identity().RepositoryRoot, ".git-drift")
	if err := os.Rename(gitDir, driftDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(driftDir, gitDir) })
	reads := authority.reads
	if _, err := b.Read(t.Context(), key); err != ErrIdentityChanged {
		t.Fatalf("lease drift = %v", err)
	}
	if authority.reads != reads || authority.closed != 0 || lock.locks != 0 {
		t.Fatal("lease drift touched storage")
	}
}

func TestWindowsRecordBackendNativeCAS(t *testing.T) {
	if testing.Short() {
		t.Skip("uses a fixture-local Git repository and Windows storage authority")
	}
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	first, err := newWindowsRecordBackend(t.Context(), lease)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := newWindowsRecordBackend(t.Context(), lease)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	store, err := NewStore(first, lease)
	if err != nil {
		t.Fatal(err)
	}
	handoff := testHandoff(t)
	record, err := store.Issue(t.Context(), handoff)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Issue(t.Context(), handoff); err != nil {
		t.Fatalf("duplicate issue = %v", err)
	}
	key := store.recordKey(handoff.Identity)
	a, b := bindFor(t, record), bindFor(t, record)
	a.SessionID, b.SessionID = "session-a", "session-b"
	a, _ = sealRecord(a)
	b, _ = sealRecord(b)
	pa, _ := a.CanonicalJSON()
	pb, _ := b.CanonicalJSON()
	start := make(chan struct{})
	type result struct {
		winner string
		err    error
	}
	results := make(chan result, 2)
	go func() { <-start; results <- result{"a", first.CompareAndSwap(t.Context(), key, record.Revision, pa)} }()
	go func() { <-start; results <- result{"b", second.CompareAndSwap(t.Context(), key, record.Revision, pb)} }()
	close(start)
	firstResult, secondResult := <-results, <-results
	if (firstResult.err == nil) == (secondResult.err == nil) {
		t.Fatalf("CAS results = %v, %v", firstResult.err, secondResult.err)
	}
	winner, loser := firstResult, secondResult
	if winner.err != nil {
		winner, loser = loser, winner
	}
	if winner.err != nil || !errors.Is(loser.err, ErrCASConflict) {
		t.Fatalf("winner = %v, loser = %v", winner.err, loser.err)
	}
	got, err := first.Read(t.Context(), key)
	want := pa
	if winner.winner == "b" {
		want = pb
	}
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("final record = %q, %v", got, err)
	}
	decoded, err := DecodeRecord(got)
	if err != nil || !recordMatchesKey(decoded, key, key.Repository) || decoded.Revision != map[string]Record{"a": a, "b": b}[winner.winner].Revision {
		t.Fatalf("final record validation = %#v, %v", decoded, err)
	}
}
