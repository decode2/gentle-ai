//go:build linux

package directrun

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLinuxRecordFilesReplaceValidationAndSuccess(t *testing.T) {
	f, lease := linuxFiles(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	key := RecordKey{f.key, digest("record", []byte("replace"))}
	path := filepath.Join(recordPaths(lease.Identity().GitCommonDir, f.key)[2], string(key.Record)[7:])
	if err := f.Replace(t.Context(), key, []byte("new")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	for _, setup := range []func() error{func() error { return os.Symlink(t.TempDir(), path) }, func() error { return os.WriteFile(path, nil, 0o644) }} {
		if err := setup(); err != nil {
			t.Fatal(err)
		}
		if err := f.Replace(t.Context(), key, []byte("new")); !errors.Is(err, ErrBackendUnavailable) {
			t.Fatalf("unsafe: %v", err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Create(t.Context(), key, []byte("old")); err != nil {
		t.Fatal(err)
	}
	order := []string{}
	o := newRecordFileOperations()
	write, syncFile, backup, replace, syncDir, unlink := o.writeAll, o.syncFile, o.backupLink, o.replace, o.syncDirectory, o.unlinkCleanup
	o.writeAll = func(fd int, b []byte) error { order = append(order, "write"); return write(fd, b) }
	o.syncFile = func(fd int) error { order = append(order, "file-fsync"); return syncFile(fd) }
	o.backupLink = func(fd int, n, b string) error { order = append(order, "backup-link"); return backup(fd, n, b) }
	o.replace = func(fd int, n, b string) error { order = append(order, "replace"); return replace(fd, n, b) }
	o.syncDirectory = func(fd int) error { order = append(order, "dir-fsync"); return syncDir(fd) }
	o.unlinkCleanup = func(fd int, n string) error { order = append(order, "unlink"); return unlink(fd, n) }
	f.operations = o
	if err := f.Replace(t.Context(), key, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(order, ","); got != "write,file-fsync,unlink,backup-link,dir-fsync,replace,dir-fsync,unlink,dir-fsync" {
		t.Fatalf("order = %s", got)
	}
	if got, err := f.Read(t.Context(), key); err != nil || string(got) != "new" {
		t.Fatalf("replace = %q, %v", got, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode: %v, %v", info, err)
	}
	if entries, err := os.ReadDir(filepath.Dir(path)); err != nil || len(entries) != 1 {
		t.Fatalf("residue: %v, %v", entries, err)
	}
}

func TestLinuxRecordFilesReplaceFaults(t *testing.T) {
	for _, test := range []struct {
		name string
		set  func(*recordFileOperations)
		new  bool
	}{
		{"write", func(o *recordFileOperations) { o.writeAll = func(int, []byte) error { return errors.New("secret") } }, false},
		{"file-fsync", func(o *recordFileOperations) { o.syncFile = func(int) error { return errors.New("secret") } }, false},
		{"backup-link", func(o *recordFileOperations) {
			o.backupLink = func(int, string, string) error { return errors.New("secret") }
		}, false},
		{"replace", func(o *recordFileOperations) {
			o.replace = func(int, string, string) error { return errors.New("secret") }
		}, false},
		{"post-replace-fsync-rolls-back", func(o *recordFileOperations) {
			calls := 0
			o.syncDirectory = func(fd int) error {
				calls++
				if calls == 2 {
					return errors.New("secret")
				}
				return unix.Fsync(fd)
			}
		}, false},
		{"rollback-unknown", func(o *recordFileOperations) {
			calls := 0
			o.syncDirectory = func(fd int) error {
				calls++
				if calls == 2 {
					return errors.New("secret")
				}
				return unix.Fsync(fd)
			}
			o.rollback = func(int, string, string) error { return errors.New("secret") }
		}, true},
		{"backup-cleanup", func(o *recordFileOperations) {
			calls := 0
			clean := o.unlinkCleanup
			o.unlinkCleanup = func(fd int, n string) error {
				calls++
				if calls == 2 {
					return errors.New("secret")
				}
				return clean(fd, n)
			}
		}, true},
		{"final-fsync", func(o *recordFileOperations) {
			calls := 0
			o.syncDirectory = func(fd int) error {
				calls++
				if calls == 3 {
					return errors.New("secret")
				}
				return unix.Fsync(fd)
			}
		}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, lease := linuxFiles(t)
			key := RecordKey{f.key, digest("record", []byte(test.name))}
			if err := f.Create(t.Context(), key, []byte("old")); err != nil {
				t.Fatal(err)
			}
			o := newRecordFileOperations()
			test.set(&o)
			f.operations = o
			err := f.Replace(t.Context(), key, []byte("new-private"))
			if !errors.Is(err, ErrBackendUnavailable) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("error: %v", err)
			}
			got, readErr := f.Read(t.Context(), key)
			if readErr != nil || (string(got) != "old" && string(got) != "new-private") {
				t.Fatalf("final = %q, %v", got, readErr)
			}
			if !test.new && string(got) != "old" {
				t.Fatalf("old changed: %q", got)
			}
			if !test.new {
				f.operations = newRecordFileOperations()
				if err := f.Replace(t.Context(), key, []byte("new-private")); err != nil {
					t.Fatalf("retry: %v", err)
				}
			}
			entries, e := os.ReadDir(recordPaths(lease.Identity().GitCommonDir, f.key)[2])
			if e != nil {
				t.Fatal(e)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".record-") {
					info, x := entry.Info()
					if x != nil || info.Mode().Perm() != 0o600 {
						t.Fatalf("private residue: %v, %v", info, x)
					}
				}
			}
		})
	}
}

func TestLinuxRecordFilesReplaceReadersAndInstanceSeams(t *testing.T) {
	f, lease := linuxFiles(t)
	reader, err := newLinuxRecordFiles(t.Context(), lease)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	key := RecordKey{f.key, digest("record", []byte("readers"))}
	if err := f.Create(t.Context(), key, []byte("old")); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	o := newRecordFileOperations()
	replace := o.replace
	o.replace = func(dir int, tmp, name string) error { close(entered); <-release; return replace(dir, tmp, name) }
	f.operations = o
	result := make(chan error, 1)
	go func() { result <- f.Replace(t.Context(), key, []byte("new")) }()
	<-entered
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, e := reader.Read(t.Context(), key)
			if e != nil || (string(got) != "old" && string(got) != "new") {
				t.Errorf("read = %q, %v", got, e)
			}
		}()
	}
	wg.Wait()
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if got, err := reader.Read(t.Context(), key); err != nil || string(got) != "new" {
		t.Fatalf("new = %q, %v", got, err)
	}
	f.operations = newRecordFileOperations()
	other, _ := linuxFiles(t)
	otherKey := RecordKey{other.key, digest("record", []byte("other-instance"))}
	if err := other.Create(t.Context(), otherKey, []byte("old")); err != nil {
		t.Fatal(err)
	}
	other.operations.replace = func(int, string, string) error { return errors.New("other seam") }
	if err := other.Replace(t.Context(), otherKey, []byte("new")); !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("other seam: %v", err)
	}
	if err := f.Replace(t.Context(), key, []byte("newer")); err != nil {
		t.Fatalf("cross-trigger: %v", err)
	}
}
