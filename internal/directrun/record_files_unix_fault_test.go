//go:build linux || darwin

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

func TestLinuxRecordFilesCreateFaults(t *testing.T) {
	for _, test := range []struct {
		name  string
		set   func(*recordFileOperations, string)
		final bool
	}{
		{"write", func(o *recordFileOperations, _ string) {
			o.writeAll = func(int, []byte) error { return errors.New("write secret") }
		}, false},
		{"file-fsync", func(o *recordFileOperations, _ string) {
			o.syncFile = func(int) error { return errors.New("sync secret") }
		}, false},
		{"publish", func(o *recordFileOperations, _ string) {
			o.publish = func(int, string, string) error { return errors.New("publish secret") }
		}, false},
		{"directory-fsync-rollback", func(o *recordFileOperations, _ string) {
			calls := 0
			o.syncDirectory = func(int) error {
				calls++
				if calls == 1 {
					return errors.New("directory secret")
				}
				return nil
			}
		}, false},
		{"rollback-unlink-unknown", func(o *recordFileOperations, final string) {
			calls := 0
			o.syncDirectory = func(int) error {
				calls++
				if calls == 1 {
					return errors.New("directory secret")
				}
				return nil
			}
			o.unlinkCleanup = func(dir int, name string) error {
				if name == final {
					return errors.New("unlink secret")
				}
				return unix.Unlinkat(dir, name, 0)
			}
		}, true},
		{"rollback-directory-fsync-unknown", func(o *recordFileOperations, _ string) {
			calls := 0
			o.syncDirectory = func(int) error { calls++; return errors.New("directory secret") }
		}, false},
		{"temp-cleanup", func(o *recordFileOperations, _ string) {
			o.writeAll = func(int, []byte) error { return errors.New("write secret") }
			o.unlinkCleanup = func(dir int, name string) error {
				if strings.HasPrefix(name, ".record-") {
					return errors.New("cleanup secret")
				}
				return unix.Unlinkat(dir, name, 0)
			}
		}, false},
		{"published-temp-cleanup", func(o *recordFileOperations, _ string) {
			calls := 0
			o.unlinkCleanup = func(dir int, name string) error {
				calls++
				if calls == 1 {
					return errors.New("cleanup secret")
				}
				return unix.Unlinkat(dir, name, 0)
			}
		}, false},
		{"published-cleanup-temp-residue", func(o *recordFileOperations, _ string) {
			calls := 0
			o.unlinkCleanup = func(dir int, name string) error {
				calls++
				if calls != 2 {
					return errors.New("cleanup secret")
				}
				return unix.Unlinkat(dir, name, 0)
			}
		}, false},
		{"published-cleanup-final-unknown", func(o *recordFileOperations, _ string) {
			o.unlinkCleanup = func(int, string) error { return errors.New("cleanup secret") }
		}, true},
		{"published-cleanup-rollback-fsync", func(o *recordFileOperations, _ string) {
			calls := 0
			o.unlinkCleanup = func(dir int, name string) error {
				calls++
				if calls == 1 {
					return errors.New("cleanup secret")
				}
				return unix.Unlinkat(dir, name, 0)
			}
			o.syncDirectory = func(int) error { return errors.New("directory secret") }
		}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			f, lease := linuxFiles(t)
			key := RecordKey{f.key, digest("record", []byte(test.name))}
			name := string(key.Record)[len("sha256:"):]
			dir := recordPaths(lease.Identity().GitCommonDir, f.key)[2]
			operations := newRecordFileOperations()
			test.set(&operations, name)
			f.operations = operations
			payload := []byte("payload must remain private")
			err := f.Create(t.Context(), key, payload)
			if !errors.Is(err, ErrBackendUnavailable) || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), lease.Identity().GitCommonDir) || strings.Contains(err.Error(), home) || strings.Contains(err.Error(), string(payload)) {
				t.Fatalf("error = %v", err)
			}
			path := filepath.Join(dir, name)
			if _, statErr := os.Stat(path); test.final != (statErr == nil) {
				t.Fatalf("final residue: %v", statErr)
			}
			entries, readErr := os.ReadDir(dir)
			if readErr != nil {
				t.Fatal(readErr)
			}
			for _, entry := range entries {
				allowTemp := test.name == "temp-cleanup" || test.name == "published-cleanup-temp-residue" || test.name == "published-cleanup-final-unknown"
				if strings.HasPrefix(entry.Name(), ".record-") && !allowTemp {
					t.Fatalf("temp residue: %s", entry.Name())
				}
				if allowTemp && strings.HasPrefix(entry.Name(), ".record-") {
					info, infoErr := entry.Info()
					if infoErr != nil || info.Mode().Perm() != 0o600 {
						t.Fatalf("temp privacy: %v %v", info, infoErr)
					}
					if test.name == "published-cleanup-final-unknown" {
						final, finalErr := os.Stat(path)
						if finalErr != nil || !os.SameFile(info, final) {
							t.Fatalf("published residue: %v %v", info, finalErr)
						}
					}
				}
			}
			healthy, healthyErr := newLinuxRecordFiles(t.Context(), lease)
			if healthyErr != nil {
				t.Fatal(healthyErr)
			}
			t.Cleanup(func() { _ = healthy.Close() })
			if test.final {
				if got, err := healthy.Read(t.Context(), key); err != nil || string(got) != string(payload) {
					t.Fatalf("unknown final = %q, %v", got, err)
				}
				if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
					t.Fatalf("unknown final mode: %v %v", info, err)
				}
				return
			}
			if err := healthy.Create(t.Context(), key, payload); err != nil {
				t.Fatalf("retry: %v", err)
			}
			if got, err := healthy.Read(t.Context(), key); err != nil || string(got) != string(payload) {
				t.Fatalf("retry = %q, %v", got, err)
			}
			if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("retry mode: %v %v", info, err)
			}
		})
	}
}

func TestLinuxRecordFilesCreateOrderingAndInstanceFaults(t *testing.T) {
	f, _ := linuxFiles(t)
	key := RecordKey{f.key, digest("record", []byte("conflict"))}
	if err := f.Create(t.Context(), key, []byte("first")); err != nil {
		t.Fatal(err)
	}
	f.operations = recordFileOperations{writeAll: func(int, []byte) error { t.Fatal("write invoked"); return nil }, syncFile: func(int) error { t.Fatal("file sync invoked"); return nil }, publish: func(int, string, string) error { t.Fatal("publish invoked"); return nil }, syncDirectory: func(int) error { t.Fatal("directory sync invoked"); return nil }, unlinkCleanup: newRecordFileOperations().unlinkCleanup}
	if err := f.Create(t.Context(), key, []byte("second")); !errors.Is(err, ErrAlreadyExists) {
		t.Fatal(err)
	}
	if got, _ := f.Read(t.Context(), key); string(got) != "first" {
		t.Fatalf("conflict changed %q", got)
	}

	ordered, _ := linuxFiles(t)
	order := make([]string, 0, 4)
	ordered.operations.writeAll = func(fd int, value []byte) error {
		order = append(order, "write")
		return newRecordFileOperations().writeAll(fd, value)
	}
	ordered.operations.syncFile = func(fd int) error { order = append(order, "file-fsync"); return unix.Fsync(fd) }
	ordered.operations.publish = func(dir int, tmp, name string) error {
		order = append(order, "publish")
		return unix.Linkat(dir, tmp, dir, name, 0)
	}
	ordered.operations.unlinkCleanup = func(dir int, name string) error {
		order = append(order, "temp-unlink")
		return unix.Unlinkat(dir, name, 0)
	}
	ordered.operations.syncDirectory = func(fd int) error { order = append(order, "directory-fsync"); return unix.Fsync(fd) }
	if err := ordered.Create(t.Context(), RecordKey{ordered.key, digest("record", []byte("order"))}, []byte("bytes")); err != nil || strings.Join(order, ",") != "write,file-fsync,publish,temp-unlink,directory-fsync" {
		t.Fatalf("order = %q, %v", order, err)
	}

	first, _ := linuxFiles(t)
	second, _ := linuxFiles(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	first.operations.writeAll = func(int, []byte) error { close(entered); <-release; return errors.New("first") }
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, file := range []*linuxRecordFiles{first, second} {
		wg.Add(1)
		go func(file *linuxRecordFiles) {
			defer wg.Done()
			err := file.Create(t.Context(), RecordKey{file.key, digest("record", []byte("parallel"))}, []byte("bytes"))
			results <- err
		}(file)
	}
	<-entered
	if err := <-results; err != nil {
		t.Fatalf("second instance: %v", err)
	}
	close(release)
	wg.Wait()
	if err := <-results; !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("first instance: %v", err)
	}
}
