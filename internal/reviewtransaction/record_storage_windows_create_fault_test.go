//go:build windows

package reviewtransaction

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/windows"
)

func TestRecordStorageAuthorityCreateRecordFaultsBeforePublication(t *testing.T) {
	for _, test := range []struct {
		name string
		set  func(*recordStorageCreateOps)
		want error
	}{
		{"write-zero", func(ops *recordStorageCreateOps) {
			ops.write = func(windows.Handle, []byte, *uint32, *windows.Overlapped) error { return nil }
		}, errRecordStorageUnavailable},
		{"write-error", func(ops *recordStorageCreateOps) {
			ops.write = func(windows.Handle, []byte, *uint32, *windows.Overlapped) error {
				return errors.New(`C:\secret\content`)
			}
		}, errRecordStorageUnavailable},
		{"file-flush", func(ops *recordStorageCreateOps) {
			ops.flush = func(windows.Handle) error { return errors.New("raw-status") }
		}, errRecordStorageUnavailable},
		{"post-write-verify", func(ops *recordStorageCreateOps) { ops.verify = func(*secureWindowsData, int) bool { return false } }, errRecordStorageUnavailable},
		{"prepublish-authority", func(ops *recordStorageCreateOps) {
			ops.validate = func(context.Context) error { return errRecordStorageAuthorityInvalid }
		}, errRecordStorageAuthorityInvalid},
		{"publish", func(ops *recordStorageCreateOps) {
			ops.publish = func(windows.Handle, windows.Handle, string) error { return errors.New("raw-status") }
		}, errRecordStorageUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			authority, lease := createAuthority(t)
			digest := faultDigest(test.name)
			ops := authority.createOps
			test.set(&ops)
			trace := &createTrace{}
			ops = trace.record(ops)
			authority.createOps = ops
			err := authority.CreateRecord(t.Context(), digest, []byte("exact"))
			if !errors.Is(err, test.want) || err.Error() != test.want.Error() {
				t.Fatalf("CreateRecord() = %v", err)
			}
			assertCreateFaultState(t, authority, lease, digest, nil)
			if test.name == "prepublish-authority" {
				trace.want(t, "temp", "create", "write", "file-flush", "postwrite-verify", "authority-validate", "cleanup")
				trace.closedOnce(t)
			}
			authority.createOps = authority.defaultCreateOps()
			if err := authority.CreateRecord(t.Context(), digest, []byte("exact")); err != nil {
				t.Fatalf("healthy retry = %v", err)
			}
			assertCreateFaultState(t, authority, lease, digest, []byte("exact"))
		})
	}
}
func TestRecordStorageAuthorityCreateRecordTempAndWriteSequences(t *testing.T) {
	authority, lease := createAuthority(t)
	ops := authority.createOps
	names := []string{"tmp-collision", "tmp-good"}
	var attempts int
	ops.tempName = func() (string, error) { name := names[attempts]; attempts++; return name, nil }
	create := ops.create
	ops.create = func(ctx context.Context, parent windows.Handle, id *secureWindowsChildID, name string) (*secureWindowsData, error) {
		if name == "tmp-collision" {
			return nil, errSecureWindowsChildExists
		}
		return create(ctx, parent, id, name)
	}
	write := ops.write
	ops.write = func(handle windows.Handle, data []byte, wrote *uint32, overlapped *windows.Overlapped) error {
		if len(data) > 1 {
			return write(handle, data[:1], wrote, overlapped)
		}
		return write(handle, data, wrote, overlapped)
	}
	authority.createOps = ops
	digest := faultDigest("short-write")
	if err := authority.CreateRecord(t.Context(), digest, []byte("exact bytes")); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("temp attempts = %d", attempts)
	}
	assertCreateFaultState(t, authority, lease, digest, []byte("exact bytes"))
	ops = authority.createOps
	ops.tempName = func() (string, error) { return "tmp-exhausted", nil }
	ops.create = func(context.Context, windows.Handle, *secureWindowsChildID, string) (*secureWindowsData, error) {
		return nil, errSecureWindowsChildExists
	}
	authority.createOps = ops
	digest = faultDigest("exhausted")
	if err := authority.CreateRecord(t.Context(), digest, nil); !errors.Is(err, errRecordStorageUnavailable) {
		t.Fatalf("exhaustion = %v", err)
	}
	assertCreateFaultState(t, authority, lease, digest, nil)
}
func TestRecordStorageAuthorityCreateRecordPublicationUnknownAndConflict(t *testing.T) {
	for _, test := range []struct {
		name string
		set  func(*recordStorageCreateOps)
	}{
		{"postpublish-verify", func(ops *recordStorageCreateOps) {
			ops.postpublish = func(context.Context, *secureWindowsData, string, int) bool { return false }
		}},
		{"post-rename-primitive", func(*recordStorageCreateOps) {}},
		{"directory-flush", func(ops *recordStorageCreateOps) {
			flush := ops.flush
			calls := 0
			ops.flush = func(h windows.Handle) error {
				calls++
				if calls == 2 {
					return errors.New("raw-status")
				}
				return flush(h)
			}
		}},
		{"close", func(ops *recordStorageCreateOps) {
			close := ops.close
			ops.close = func(data *secureWindowsData) error { _ = close(data); return errors.New("raw-status") }
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			authority, lease := createAuthority(t)
			digest := faultDigest(test.name)
			ops := authority.createOps
			test.set(&ops)
			if test.name == "post-rename-primitive" {
				ops.publish = func(source, destination windows.Handle, name string) error {
					return publishWindowsNoReplaceRelativeAfter(source, destination, name, func() { _ = windows.CloseHandle(destination) })
				}
			}
			trace := &createTrace{}
			ops = trace.record(ops)
			authority.createOps = ops
			err := authority.CreateRecord(t.Context(), digest, []byte("exact"))
			if !errors.Is(err, errRecordStoragePublicationUnknown) {
				t.Fatalf("CreateRecord() = %v", err)
			}
			assertCreateFaultState(t, authority, lease, digest, []byte("exact"))
			if test.name == "postpublish-verify" {
				trace.want(t, "temp", "create", "write", "file-flush", "postwrite-verify", "authority-validate", "publish", "postpublish-verify", "close")
				trace.closedOnce(t)
			}
			if test.name == "post-rename-primitive" {
				trace.want(t, "temp", "create", "write", "file-flush", "postwrite-verify", "authority-validate", "publish", "close")
				trace.closedOnce(t)
			}
			if test.name == "directory-flush" {
				trace.want(t, "temp", "create", "write", "file-flush", "postwrite-verify", "authority-validate", "publish", "postpublish-verify", "directory-flush", "close")
			}
			retry := authority
			if test.name == "post-rename-primitive" {
				retry = createAuthorityForLease(t, lease)
			}
			if err := retry.CreateRecord(t.Context(), digest, []byte("other")); !errors.Is(err, errRecordStorageExists) {
				t.Fatalf("reconciliation retry = %v", err)
			}
		})
	}
}
func TestRecordStorageAuthorityCreateRecordOrderAndConflictClose(t *testing.T) {
	authority, lease := createAuthority(t)
	digest := faultDigest("order")
	trace := &createTrace{}
	authority.createOps = trace.record(authority.createOps)
	if err := authority.CreateRecord(t.Context(), digest, nil); err != nil {
		t.Fatal(err)
	}
	trace.want(t, "temp", "create", "file-flush", "postwrite-verify", "authority-validate", "publish", "postpublish-verify", "directory-flush", "close")
	trace.closedOnce(t)
	trace.calls, trace.closes = nil, 0
	if err := authority.CreateRecord(t.Context(), digest, []byte("other")); !errors.Is(err, errRecordStorageExists) {
		t.Fatalf("conflict = %v", err)
	}
	trace.want(t, "temp", "create", "write", "file-flush", "postwrite-verify", "authority-validate", "publish", "cleanup")
	trace.closedOnce(t)
	assertCreateFaultState(t, authority, lease, digest, []byte{})
}
func TestRecordStorageAuthorityCreateRecordCleanupAndAuthorityIsolation(t *testing.T) {
	first, firstLease := createAuthority(t)
	second, secondLease := createAuthority(t)
	ops := first.createOps
	cleanup := ops.cleanup
	closed := false
	ops.cleanup = func(data *secureWindowsData) error {
		_ = cleanup(data)
		closed = data.handle == 0
		return errors.New("raw-status")
	}
	first.createOps = ops
	digest := faultDigest("cleanup")
	first.createOps.write = func(windows.Handle, []byte, *uint32, *windows.Overlapped) error { return nil }
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, authority := range []*RecordStorageAuthority{first, second} {
		group.Add(1)
		go func(a *RecordStorageAuthority) {
			defer group.Done()
			results <- a.CreateRecord(t.Context(), digest, []byte("exact"))
		}(authority)
	}
	group.Wait()
	close(results)
	var unavailable, success int
	for err := range results {
		if errors.Is(err, errRecordStorageUnavailable) {
			unavailable++
		}
		if err == nil {
			success++
		}
	}
	if unavailable != 1 || success != 1 {
		t.Fatalf("isolated results = %d unavailable, %d success", unavailable, success)
	}
	if !closed {
		t.Fatal("cleanup did not close temp")
	}
	assertCreateFaultState(t, first, firstLease, digest, nil)
	assertCreateFaultState(t, second, secondLease, digest, []byte("exact"))
}
func faultDigest(seed string) string {
	var total byte
	for i := range seed {
		total += seed[i]
	}
	return strings.Repeat("0", 63) + string("0123456789abcdef"[total%16])
}
func assertCreateFaultState(t *testing.T, authority *RecordStorageAuthority, lease *RepositoryIdentityLease, digest string, want []byte) {
	t.Helper()
	_ = authority
	dir := filepath.Join(lease.Identity().GitCommonDir, recordStorageGentleAI, recordStorageRecords, lease.StorageKey())
	reader := createAuthorityForLease(t, lease)
	if want == nil {
		if _, err := reader.ReadRecord(t.Context(), digest, 1<<20); !errors.Is(err, errRecordStorageMissing) {
			t.Fatalf("unexpected final = %v", err)
		}
	} else if got, err := reader.ReadRecord(t.Context(), digest, 1<<20); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("final = %q, %v", got, err)
	}
	assertCreateNoTemps(t, dir)
}

type createTrace struct {
	calls  []string
	closes int
}

func (trace *createTrace) add(name string) { trace.calls = append(trace.calls, name) }
func (trace *createTrace) record(ops recordStorageCreateOps) recordStorageCreateOps {
	temp, create, write, flush := ops.tempName, ops.create, ops.write, ops.flush
	verify, validate, publish, postpublish := ops.verify, ops.validate, ops.publish, ops.postpublish
	cleanup, close := ops.cleanup, ops.close
	flushes := 0
	ops.tempName = func() (string, error) { trace.add("temp"); return temp() }
	ops.create = func(c context.Context, p windows.Handle, i *secureWindowsChildID, n string) (*secureWindowsData, error) {
		trace.add("create")
		return create(c, p, i, n)
	}
	ops.write = func(h windows.Handle, b []byte, n *uint32, o *windows.Overlapped) error {
		trace.add("write")
		return write(h, b, n, o)
	}
	ops.flush = func(h windows.Handle) error {
		name := "file-flush"
		if flushes > 0 {
			name = "directory-flush"
		}
		flushes++
		trace.add(name)
		return flush(h)
	}
	ops.verify = func(data *secureWindowsData, size int) bool { trace.add("postwrite-verify"); return verify(data, size) }
	ops.validate = func(ctx context.Context) error { trace.add("authority-validate"); return validate(ctx) }
	ops.publish = func(s, d windows.Handle, n string) error { trace.add("publish"); return publish(s, d, n) }
	ops.postpublish = func(c context.Context, d *secureWindowsData, n string, s int) bool {
		trace.add("postpublish-verify")
		return postpublish(c, d, n, s)
	}
	ops.cleanup = func(data *secureWindowsData) error {
		trace.add("cleanup")
		err := cleanup(data)
		if data.handle == 0 {
			trace.closes++
		}
		return err
	}
	ops.close = func(data *secureWindowsData) error { trace.add("close"); trace.closes++; return close(data) }
	return ops
}
func (trace *createTrace) want(t *testing.T, want ...string) {
	t.Helper()
	if !slices.Equal(trace.calls, want) {
		t.Fatalf("calls = %q, want %q", trace.calls, want)
	}
}
func (trace *createTrace) closedOnce(t *testing.T) {
	t.Helper()
	if trace.closes != 1 {
		t.Fatalf("temp closes = %d", trace.closes)
	}
}
