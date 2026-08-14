//go:build windows

package reviewtransaction

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/windows"
)

type lockOpsFake struct {
	mu                                               sync.Mutex
	order                                            []string
	createErr, closeErr, lockErr, cancelErr, waitErr error
	results                                          []error
	status                                           uint32
	unlockErr                                        error
	lockO, cancelO, resultO, unlockO                 *windows.Overlapped
	canceled                                         chan struct{}
}

func (f *lockOpsFake) add(stage string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.order = append(f.order, stage)
}
func (f *lockOpsFake) ops() recordStorageLockOps {
	return recordStorageLockOps{
		createEvent: func() (windows.Handle, error) { f.add("create"); return 9, f.createErr },
		closeEvent:  func(windows.Handle) error { f.add("close"); return f.closeErr },
		lock: func(_ windows.Handle, _ uint32, _ uint32, _ uint32, _ uint32, o *windows.Overlapped) error {
			f.add("lock")
			f.lockO = o
			return f.lockErr
		},
		cancel: func(_ windows.Handle, o *windows.Overlapped) error {
			f.add("cancel")
			f.cancelO = o
			if f.canceled != nil {
				close(f.canceled)
			}
			return f.cancelErr
		},
		wait: func(windows.Handle, uint32) (uint32, error) { f.add("wait"); return f.status, f.waitErr },
		result: func(_ windows.Handle, o *windows.Overlapped, _ *uint32, wait bool) error {
			f.add(map[bool]string{true: "result-wait", false: "result"}[wait])
			f.resultO = o
			if len(f.results) == 0 {
				return nil
			}
			err := f.results[0]
			f.results = f.results[1:]
			return err
		},
		unlock: func(_ windows.Handle, _ uint32, _ uint32, _ uint32, o *windows.Overlapped) error {
			f.add("unlock")
			f.unlockO = o
			return f.unlockErr
		},
	}
}

func TestRecordStorageLockRangeFaultMatrix(t *testing.T) {
	secret := errors.New("root/path HOME .record.lock raw 31337 0xc0ffee42")
	for _, token := range []string{"root/path", "HOME", ".record.lock", "raw", "31337", "0xc0ffee42"} {
		if !strings.Contains(secret.Error(), token) {
			t.Fatalf("fake error missing %q", token)
		}
	}
	for _, tt := range []struct {
		name      string
		set       func(*lockOpsFake)
		want      error
		uncertain bool
		order     string
	}{
		{"create event", func(f *lockOpsFake) { f.createErr = secret }, errRecordStorageUnavailable, false, "create"},
		{"immediate failure", func(f *lockOpsFake) { f.lockErr = secret }, errRecordStorageUnavailable, false, "create lock close"},
		{"immediate lease", func(*lockOpsFake) {}, nil, false, "create lock close"},
		{"pending lease", func(f *lockOpsFake) { f.lockErr = windows.ERROR_IO_PENDING; f.status = windows.WAIT_OBJECT_0 }, nil, false, "create lock wait result close"},
		{"wait error acquired", func(f *lockOpsFake) { f.lockErr = windows.ERROR_IO_PENDING; f.waitErr = secret }, errRecordStorageUnavailable, false, "create lock wait cancel result-wait unlock close"},
		{"wait error unknown poison", func(f *lockOpsFake) {
			f.lockErr = windows.ERROR_IO_PENDING
			f.waitErr = secret
			f.results = []error{secret}
			f.unlockErr = secret
		}, errRecordStorageUnavailable, true, "create lock wait cancel result-wait unlock close"},
		{"wait status aborted", func(f *lockOpsFake) {
			f.lockErr = windows.ERROR_IO_PENDING
			f.status = windows.WAIT_FAILED
			f.results = []error{windows.ERROR_OPERATION_ABORTED}
		}, errRecordStorageUnavailable, false, "create lock wait cancel result-wait close"},
		{"wait status unknown", func(f *lockOpsFake) {
			f.lockErr = windows.ERROR_IO_PENDING
			f.status = windows.WAIT_FAILED
			f.results = []error{secret}
		}, errRecordStorageUnavailable, false, "create lock wait cancel result-wait unlock close"},
		{"result unknown", func(f *lockOpsFake) {
			f.lockErr = windows.ERROR_IO_PENDING
			f.status = windows.WAIT_OBJECT_0
			f.results = []error{secret}
		}, errRecordStorageUnavailable, false, "create lock wait result unlock close"},
		{"result unknown poison", func(f *lockOpsFake) {
			f.lockErr = windows.ERROR_IO_PENDING
			f.status = windows.WAIT_OBJECT_0
			f.results = []error{secret}
			f.unlockErr = secret
		}, errRecordStorageUnavailable, true, "create lock wait result unlock close"},
		{"event close acquired", func(f *lockOpsFake) { f.closeErr = secret }, errRecordStorageUnavailable, false, "create lock close unlock"},
		{"event close poison", func(f *lockOpsFake) { f.closeErr = secret; f.unlockErr = secret }, errRecordStorageUnavailable, true, "create lock close unlock"},
		{"event close pre-acquired", func(f *lockOpsFake) { f.lockErr = secret; f.closeErr = secret }, errRecordStorageUnavailable, false, "create lock close"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := &lockOpsFake{}
			tt.set(f)
			got := lockRecordStorageRange(t.Context(), 7, f.ops())
			if !errors.Is(got.err, tt.want) || got.uncertain != tt.uncertain {
				t.Fatalf("result = %#v", got)
			}
			if got.err != nil && strings.Contains(got.err.Error(), "root/path") {
				t.Fatal("native error leaked")
			}
			if actual := strings.Join(f.order, " "); actual != tt.order {
				t.Fatalf("order = %q, want %q", actual, tt.order)
			}
		})
	}
	t.Run("immediate canceled", func(t *testing.T) {
		f := &lockOpsFake{}
		ctx, cancel := context.WithCancel(t.Context())
		ops := f.ops()
		ops.lock = func(_ windows.Handle, _ uint32, _ uint32, _ uint32, _ uint32, o *windows.Overlapped) error {
			f.add("lock")
			f.lockO = o
			cancel()
			return nil
		}
		got := lockRecordStorageRange(ctx, 7, ops)
		if !errors.Is(got.err, context.Canceled) || got.uncertain {
			t.Fatalf("result = %#v", got)
		}
		if actual := strings.Join(f.order, " "); actual != "create lock unlock close" {
			t.Fatalf("order = %q", actual)
		}
		if f.unlockO == f.lockO || f.unlockO.HEvent != 0 || f.unlockO.Offset != 0 || f.unlockO.OffsetHigh != 0 {
			t.Fatal("unlock did not use a zeroed, independent OVERLAPPED")
		}
	})
	t.Run("cancellation completion matrix", func(t *testing.T) {
		for _, tt := range []struct {
			name              string
			cancelErr, result error
			order             string
		}{
			{"ordinary", nil, windows.ERROR_OPERATION_ABORTED, "create lock wait cancel result close"},
			{"not found acquired", windows.ERROR_NOT_FOUND, nil, "create lock wait cancel result unlock close"},
			{"other aborted", secret, windows.ERROR_OPERATION_ABORTED, "create lock wait cancel result close"},
			{"other acquired", secret, nil, "create lock wait cancel result unlock close"},
		} {
			t.Run(tt.name, func(t *testing.T) {
				f := &lockOpsFake{lockErr: windows.ERROR_IO_PENDING, status: windows.WAIT_OBJECT_0, cancelErr: tt.cancelErr, results: []error{tt.result}, canceled: make(chan struct{})}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				ops := f.ops()
				ops.wait = func(windows.Handle, uint32) (uint32, error) {
					f.add("wait")
					cancel()
					<-f.canceled
					return windows.WAIT_OBJECT_0, nil
				}
				got := lockRecordStorageRange(ctx, 7, ops)
				if !errors.Is(got.err, context.Canceled) || got.uncertain {
					t.Fatalf("result = %#v", got)
				}
				if f.cancelO != f.lockO || f.resultO != f.lockO {
					t.Fatal("completion did not use the acquisition OVERLAPPED")
				}
				if actual := strings.Join(f.order, " "); actual != tt.order {
					t.Fatalf("order = %q", actual)
				}
			})
		}
	})
}

func TestRecordStorageLockPoisonAndClose(t *testing.T) {
	a, _ := createAuthority(t)
	l := recordLock(t, a)
	f := &lockOpsFake{unlockErr: errors.New("unlock")}
	l.ops = f.ops()
	release, err := l.Lock(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); !errors.Is(err, errRecordStorageUnavailable) || !l.closed {
		t.Fatalf("release = %v, closed = %v", err, l.closed)
	}
	if err := release(); !errors.Is(err, errRecordStorageUnavailable) {
		t.Fatalf("repeat release = %v", err)
	}
	if _, err := l.Lock(t.Context()); !errors.Is(err, errRecordStorageUnavailable) {
		t.Fatalf("same lock = %v", err)
	}
	other := recordLock(t, a)
	release, err = other.Lock(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	a, _ = createAuthority(t)
	l = recordLock(t, a)
	f = &lockOpsFake{unlockErr: errors.New("unlock")}
	ops := f.ops()
	ops.lock = func(_ windows.Handle, _ uint32, _ uint32, _ uint32, _ uint32, o *windows.Overlapped) error {
		f.add("lock")
		f.lockO = o
		a.storageKey = strings.Repeat("0", 64)
		return nil
	}
	l.ops = ops
	if _, err := l.Lock(t.Context()); !errors.Is(err, errRecordStorageUnavailable) || !l.closed {
		t.Fatalf("post-acquire validation = %v, closed = %v", err, l.closed)
	}
	a, _ = createAuthority(t)
	l = recordLock(t, a)
	closes := 0
	l.closeData = func(data *secureWindowsData) error { closes++; data.handle = 0; return errors.New("close") }
	if err := l.Close(); !errors.Is(err, errRecordStorageUnavailable) {
		t.Fatalf("close = %v", err)
	}
	if err := l.Close(); err != nil || closes != 1 {
		t.Fatalf("repeat = %v, closes = %d", err, closes)
	}
}
