//go:build linux

package directrun

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

func TestLinuxRecordFiles(t *testing.T) {
	f, lease := linuxFiles(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	key := RecordKey{f.key, digest("record", []byte("one"))}
	dir := filepath.Join(lease.Identity().GitCommonDir, "gentle-ai", "direct-run-records", string(f.key)[7:])
	for _, p := range []string{filepath.Dir(filepath.Dir(dir)), filepath.Dir(dir), dir} {
		if info, err := os.Stat(p); err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("directory %q mode: %v %v", p, info, err)
		}
	}
	if got, err := f.Read(t.Context(), key); !errors.Is(err, ErrNotFound) || got != nil {
		t.Fatalf("missing = %q, %v", got, err)
	}
	if err := f.Create(t.Context(), key, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if got, err := f.Read(t.Context(), key); err != nil || string(got) != "first" {
		t.Fatalf("read = %q, %v", got, err)
	}
	if info, err := os.Stat(filepath.Join(dir, string(key.Record)[7:])); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("record mode: %v %v", info, err)
	}
	if err := f.Create(t.Context(), key, []byte("second")); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("conflict: %v", err)
	}
	if got, _ := f.Read(t.Context(), key); string(got) != "first" {
		t.Fatalf("conflict changed %q", got)
	}
	if err := f.Create(t.Context(), RecordKey{f.key, digest("record", []byte("large"))}, make([]byte, maxRecordBytes+1)); !errors.Is(err, ErrRecordTooLarge) {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
		t.Fatalf("oversized residue: %v %v", entries, err)
	}
	large := RecordKey{f.key, digest("record", []byte("read-large"))}
	if err := os.WriteFile(filepath.Join(dir, string(large.Record)[7:]), make([]byte, maxRecordBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Read(t.Context(), large); !errors.Is(err, ErrRecordTooLarge) {
		t.Fatal(err)
	}
	if _, err := f.Read(t.Context(), RecordKey{digest("other", nil), key.Record}); !errors.Is(err, ErrIdentityChanged) {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Read(t.Context(), key); !errors.Is(err, ErrIdentityChanged) || strings.Contains(err.Error(), home) {
		t.Fatalf("closed error: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("double close: %v", err)
	}
}

func TestLinuxRecordFilesCloseConcurrent(t *testing.T) {
	f, _ := linuxFiles(t)
	key := RecordKey{f.key, digest("record", []byte("close"))}
	if err := f.Create(t.Context(), key, []byte("bytes")); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := f.Close(); err != nil {
				t.Errorf("close: %v", err)
			}
		}()
	}
	wg.Wait()
	if _, err := f.Read(t.Context(), key); !errors.Is(err, ErrIdentityChanged) {
		t.Fatalf("read after close: %v", err)
	}
	if err := f.Create(t.Context(), key, []byte("other")); !errors.Is(err, ErrIdentityChanged) {
		t.Fatalf("create after close: %v", err)
	}
}

func TestLinuxRecordFilesCloseSerializesOperations(t *testing.T) {
	f, _ := linuxFiles(t)
	key := RecordKey{f.key, digest("record", []byte("parallel"))}
	if err := f.Create(t.Context(), key, []byte("bytes")); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, op := range []func(){func() { _, _ = f.Read(t.Context(), key) }, func() {
		_ = f.Create(t.Context(), RecordKey{f.key, digest("record", []byte("other"))}, []byte("bytes"))
	}, func() { _ = f.Close() }} {
		wg.Add(1)
		go func(op func()) { defer wg.Done(); <-start; op() }(op)
	}
	close(start)
	wg.Wait()
	if _, err := f.Read(t.Context(), key); !errors.Is(err, ErrIdentityChanged) {
		t.Fatalf("authority survived close: %v", err)
	}
}

func linuxFiles(t *testing.T) (*linuxRecordFiles, *reviewtransaction.RepositoryIdentityLease) {
	t.Helper()
	repo := t.TempDir()
	cmd := exec.Command("git", "init", repo)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	f, err := newLinuxRecordFiles(t.Context(), lease)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f, lease
}
