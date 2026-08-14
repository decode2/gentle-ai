//go:build windows

package reviewtransaction

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/windows"
)

const (
	replaceOld    = "OLD-PAYLOAD-SENTINEL"
	replaceNew    = "NEW-PAYLOAD-SENTINEL"
	replaceTemp   = "tmp-REPLACE-TEMP-SENTINEL"
	replaceHome   = "HOME-SENTINEL"
	replaceRoot   = `C:\ROOT-SENTINEL`
	replaceRaw    = "RAW-INJECTED-SENTINEL"
	replaceStatus = "0xc0ffee42"
)

func TestRecordStorageAuthorityReplaceRecordPrepublicationFaults(t *testing.T) {
	for _, test := range []struct {
		name   string
		set    func(*RecordStorageAuthority, error)
		want   error
		poison bool
	}{
		{"initial-missing", func(a *RecordStorageAuthority, _ error) {
			a.replaceOps.open = func(context.Context, windows.Handle, *secureWindowsChildID, string) (*secureWindowsData, error) {
				return nil, errSecureWindowsChildMissing
			}
		}, errRecordStorageMissing, false},
		{"initial-refusal", func(a *RecordStorageAuthority, poison error) {
			a.replaceOps.open = func(context.Context, windows.Handle, *secureWindowsChildID, string) (*secureWindowsData, error) {
				return nil, poison
			}
		}, errRecordStorageAuthorityInvalid, true},
		{"temp-exhaustion", func(a *RecordStorageAuthority, _ error) {
			a.createOps.create = func(context.Context, windows.Handle, *secureWindowsChildID, string) (*secureWindowsData, error) {
				return nil, errSecureWindowsChildExists
			}
		}, errRecordStorageUnavailable, false},
		{"temp-name-unavailable", func(a *RecordStorageAuthority, poison error) {
			a.createOps.tempName = func() (string, error) { return "", poison }
		}, errRecordStorageUnavailable, true},
		{"write-zero", func(a *RecordStorageAuthority, _ error) {
			a.createOps.write = func(windows.Handle, []byte, *uint32, *windows.Overlapped) error { return nil }
		}, errRecordStorageUnavailable, false},
		{"file-flush", func(a *RecordStorageAuthority, poison error) {
			a.createOps.flush = func(windows.Handle) error { return poison }
		}, errRecordStorageUnavailable, true},
		{"postwrite-verify", func(a *RecordStorageAuthority, _ error) {
			a.createOps.verify = func(*secureWindowsData, int) bool { return false }
		}, errRecordStorageUnavailable, false},
		{"authority-drift", func(a *RecordStorageAuthority, _ error) {
			a.createOps.validate = func(context.Context) error { return errRecordStorageAuthorityInvalid }
		}, errRecordStorageAuthorityInvalid, false},
		{"destination-recheck-missing", func(a *RecordStorageAuthority, _ error) {
			open := a.replaceOps.open
			calls := 0
			a.replaceOps.open = func(ctx context.Context, h windows.Handle, id *secureWindowsChildID, name string) (*secureWindowsData, error) {
				calls++
				if calls == 2 {
					return nil, errSecureWindowsChildMissing
				}
				return open(ctx, h, id, name)
			}
		}, errRecordStorageMissing, false},
		{"native-status", func(a *RecordStorageAuthority, poison error) {
			a.replaceOps.publish = func(windows.Handle, windows.Handle, string, secureWindowsChildID) error {
				return poison
			}
		}, errRecordStorageUnavailable, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			authority, lease := createAuthority(t)
			digest := strings.Repeat("d", 64)
			poison := replacePoison(t, digest)
			if err := authority.CreateRecord(t.Context(), digest, []byte(replaceOld)); err != nil {
				t.Fatal(err)
			}
			authority.createOps.tempName = func() (string, error) { return replaceTemp, nil }
			test.set(authority, poison)
			err := authority.ReplaceRecord(t.Context(), digest, []byte(replaceNew))
			if !errors.Is(err, test.want) || err.Error() != test.want.Error() {
				t.Fatalf("ReplaceRecord() = %v", err)
			}
			if test.poison {
				assertReplaceRedacted(t, err, replacePoisonTokens(digest)...)
			}
			assertReplaceFaultState(t, lease, digest, []byte(replaceOld))
			authority.createOps = authority.defaultCreateOps()
			authority.replaceOps = authority.defaultReplaceOps()
			if err := authority.ReplaceRecord(t.Context(), digest, []byte(replaceNew)); err != nil {
				t.Fatalf("healthy retry = %v", err)
			}
			assertReplaceFaultState(t, lease, digest, []byte(replaceNew))
		})
	}
}

func TestRecordStorageAuthorityReplaceRecordTempSequencesAndResidue(t *testing.T) {
	authority, lease := createAuthority(t)
	digest := strings.Repeat("0", 63) + "1"
	if err := authority.CreateRecord(t.Context(), digest, []byte("old")); err != nil {
		t.Fatal(err)
	}
	ops, names, attempts := authority.createOps, []string{"tmp-collision", "tmp-good"}, 0
	create, write := ops.create, ops.write
	ops.tempName = func() (string, error) { name := names[attempts]; attempts++; return name, nil }
	ops.create = func(ctx context.Context, h windows.Handle, id *secureWindowsChildID, name string) (*secureWindowsData, error) {
		if name == "tmp-collision" {
			return nil, errSecureWindowsChildExists
		}
		return create(ctx, h, id, name)
	}
	ops.write = func(h windows.Handle, b []byte, n *uint32, o *windows.Overlapped) error {
		if len(b) > 1 {
			return write(h, b[:1], n, o)
		}
		return write(h, b, n, o)
	}
	authority.createOps = ops
	if err := authority.ReplaceRecord(t.Context(), digest, []byte("exact new")); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("temp attempts = %d", attempts)
	}
	assertReplaceFaultState(t, lease, digest, []byte("exact new"))

	digest = strings.Repeat("0", 63) + "2"
	if err := authority.CreateRecord(t.Context(), digest, []byte("old")); err != nil {
		t.Fatal(err)
	}
	ops = authority.defaultCreateOps()
	ops.tempName = func() (string, error) { return "tmp-residue", nil }
	ops.write = func(windows.Handle, []byte, *uint32, *windows.Overlapped) error { return nil }
	ops.cleanup = func(data *secureWindowsData) error { _ = data.Close(); return errors.New("cleanup") }
	authority.createOps = ops
	if err := authority.ReplaceRecord(t.Context(), digest, []byte("new")); !errors.Is(err, errRecordStorageUnavailable) {
		t.Fatalf("cleanup = %v", err)
	}
	got, err := createAuthorityForLease(t, lease).ReadRecord(t.Context(), digest, 1<<20)
	if err != nil || !bytes.Equal(got, []byte("old")) {
		t.Fatalf("final = %q, %v", got, err)
	}
	path := filepath.Join(lease.Identity().GitCommonDir, recordStorageGentleAI, recordStorageRecords, lease.StorageKey(), "tmp-residue")
	if err := validatePrivateRARFile(path); err != nil {
		t.Fatalf("private residue = %v", err)
	}
}

func TestRecordStorageAuthorityReplaceRecordPublicationUnknown(t *testing.T) {
	for _, test := range []struct {
		name   string
		set    func(*RecordStorageAuthority, error)
		poison bool
	}{
		{"postpublish-verify", func(a *RecordStorageAuthority, _ error) {
			a.createOps.postpublish = func(context.Context, *secureWindowsData, string, int) bool { return false }
		}, false},
		{"directory-flush", func(a *RecordStorageAuthority, poison error) {
			flush, calls := a.createOps.flush, 0
			a.createOps.flush = func(h windows.Handle) error {
				calls++
				if calls == 2 {
					return poison
				}
				return flush(h)
			}
		}, true},
		{"close", func(a *RecordStorageAuthority, poison error) {
			close := a.createOps.close
			a.createOps.close = func(data *secureWindowsData) error { _ = close(data); return poison }
		}, true},
		{"after-rename", func(a *RecordStorageAuthority, _ error) {
			a.replaceOps.publish = func(s, d windows.Handle, n string, id secureWindowsChildID) error {
				return publishWindowsRelativeAfter(s, d, n, true, id, func() { _ = windows.CloseHandle(d) })
			}
		}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			authority, lease := createAuthority(t)
			digest := strings.Repeat("e", 64)
			poison := replacePoison(t, digest)
			if err := authority.CreateRecord(t.Context(), digest, []byte(replaceOld)); err != nil {
				t.Fatal(err)
			}
			authority.createOps.tempName = func() (string, error) { return replaceTemp, nil }
			test.set(authority, poison)
			err := authority.ReplaceRecord(t.Context(), digest, []byte(replaceNew))
			if !errors.Is(err, errRecordStoragePublicationUnknown) || err.Error() != errRecordStoragePublicationUnknown.Error() {
				t.Fatalf("ReplaceRecord() = %v", err)
			}
			if test.poison {
				assertReplaceRedacted(t, err, replacePoisonTokens(digest)...)
			}
			assertReplaceFaultState(t, lease, digest, []byte(replaceNew))
			retry := createAuthorityForLease(t, lease)
			if err := retry.ReplaceRecord(t.Context(), digest, []byte(replaceNew)); err != nil {
				t.Fatalf("idempotent retry = %v", err)
			}
			assertReplaceFaultState(t, lease, digest, []byte(replaceNew))
		})
	}
}

func TestRecordStorageAuthorityReplaceRecordOrderAndAuthorityIsolation(t *testing.T) {
	t.Run("stage-order", func(t *testing.T) {
		authority, _ := createAuthority(t)
		digest := faultDigest("replace-order")
		if err := authority.CreateRecord(t.Context(), digest, []byte("old")); err != nil {
			t.Fatal(err)
		}
		trace := &createTrace{}
		authority.createOps = trace.record(authority.createOps)
		authority.replaceOps = trace.recordReplace(authority.replaceOps)
		if err := authority.ReplaceRecord(t.Context(), digest, []byte("new")); err != nil {
			t.Fatal(err)
		}
		trace.want(t, "destination-open", "temp", "create", "write", "file-flush", "postwrite-verify", "authority-validate", "destination-open", "native-replace", "postpublish-verify", "directory-flush", "close")
	})
	lease, err := OpenRepositoryIdentityLease(t.Context(), initSnapshotRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	first, second := createAuthorityForLease(t, lease), createAuthorityForLease(t, lease)
	digest := faultDigest("replace-isolation")
	if err := first.CreateRecord(t.Context(), digest, []byte("old")); err != nil {
		t.Fatal(err)
	}
	first.replaceOps.open = func(context.Context, windows.Handle, *secureWindowsChildID, string) (*secureWindowsData, error) {
		return nil, errSecureWindowsChildMissing
	}
	start, results := make(chan struct{}), make(chan error, 2)
	var group sync.WaitGroup
	for _, authority := range []*RecordStorageAuthority{first, second} {
		group.Add(1)
		go func(a *RecordStorageAuthority) {
			defer group.Done()
			<-start
			results <- a.ReplaceRecord(t.Context(), digest, []byte("exact new"))
		}(authority)
	}
	close(start)
	group.Wait()
	close(results)
	var missing, success int
	for err := range results {
		if errors.Is(err, errRecordStorageMissing) {
			missing++
		}
		if err == nil {
			success++
		}
	}
	if missing != 1 || success != 1 {
		t.Fatalf("results = %d missing, %d success", missing, success)
	}
	assertReplaceFaultState(t, lease, digest, []byte("exact new"))
}

func assertReplaceFaultState(t *testing.T, lease *RepositoryIdentityLease, digest string, want []byte) {
	t.Helper()
	got, err := createAuthorityForLease(t, lease).ReadRecord(t.Context(), digest, 1<<20)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("final = %q, %v", got, err)
	}
	assertCreateNoTemps(t, filepath.Join(lease.Identity().GitCommonDir, recordStorageGentleAI, recordStorageRecords, lease.StorageKey()))
}

func assertReplaceRedacted(t *testing.T, err error, tokens ...string) {
	t.Helper()
	for _, token := range tokens {
		if strings.Contains(err.Error(), token) {
			t.Fatalf("unredacted error: %v", err)
		}
	}
}

func replacePoison(t *testing.T, digest string) error {
	t.Helper()
	poison := errors.New(strings.Join(replacePoisonTokens(digest), "|"))
	for _, token := range replacePoisonTokens(digest) {
		if !strings.Contains(poison.Error(), token) {
			t.Fatalf("poison omits %q", token)
		}
	}
	return poison
}

func replacePoisonTokens(digest string) []string {
	return []string{replaceOld, replaceNew, digest, replaceTemp, replaceHome, replaceRoot, replaceRaw, "STATUS_POISON", "31337", replaceStatus}
}

func (trace *createTrace) recordReplace(ops recordStorageReplaceOps) recordStorageReplaceOps {
	open, publish := ops.open, ops.publish
	ops.open = func(ctx context.Context, h windows.Handle, id *secureWindowsChildID, name string) (*secureWindowsData, error) {
		trace.add("destination-open")
		return open(ctx, h, id, name)
	}
	ops.publish = func(s, d windows.Handle, n string, id secureWindowsChildID) error {
		trace.add("native-replace")
		return publish(s, d, n, id)
	}
	return ops
}
