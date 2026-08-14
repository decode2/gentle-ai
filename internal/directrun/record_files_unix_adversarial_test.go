//go:build linux || darwin

package directrun

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
	"golang.org/x/sys/unix"
)

func TestLinuxRecordFilesRejectsReplacedNamespaces(t *testing.T) {
	for _, component := range []int{0, 1, 2} {
		t.Run([]string{"vendor", "store", "repository"}[component], func(t *testing.T) {
			f, lease := linuxFiles(t)
			key := RecordKey{f.key, digest("record", []byte("replacement"))}
			paths := recordPaths(lease.Identity().GitCommonDir, f.key)
			replacement := paths[component] + ".old"
			if err := os.Rename(paths[component], replacement); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(paths[component], 0o700); err != nil {
				t.Fatal(err)
			}
			if _, err := f.Read(t.Context(), key); !errors.Is(err, ErrBackendUnavailable) {
				t.Fatalf("read: %v", err)
			}
			if err := f.Create(t.Context(), key, []byte("secret")); !errors.Is(err, ErrBackendUnavailable) {
				t.Fatalf("create: %v", err)
			}
			if _, err := os.Stat(filepath.Join(paths[2], string(key.Record)[7:])); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("replacement written: %v", err)
			}
		})
	}
}

func TestLinuxRecordFilesRejectsNamespaceTypesAndModes(t *testing.T) {
	for _, attack := range []struct {
		name  string
		setup func(t *testing.T, path string)
	}{
		{"symlink", func(t *testing.T, p string) {
			if err := os.Symlink(t.TempDir(), p); err != nil {
				t.Fatal(err)
			}
		}},
		{"file", func(t *testing.T, p string) {
			if err := os.WriteFile(p, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"weak-mode", func(t *testing.T, p string) {
			if err := os.Mkdir(p, 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{"special-mode", func(t *testing.T, p string) {
			if err := os.Mkdir(p, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := unix.Chmod(p, 0o2700); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		for _, component := range []int{0, 1, 2} {
			t.Run(attack.name+"-"+string(rune('0'+component)), func(t *testing.T) {
				repo := t.TempDir()
				lease := adversarialLease(t, repo)
				key := digest("gentle-ai.direct-run-store/v1", []byte(lease.StorageKey()))
				paths := recordPaths(lease.Identity().GitCommonDir, key)
				if component > 0 {
					if err := os.MkdirAll(filepath.Dir(paths[component]), 0o700); err != nil {
						t.Fatal(err)
					}
				}
				attack.setup(t, paths[component])
				if f, err := newLinuxRecordFiles(t.Context(), lease); f != nil || !errors.Is(err, ErrBackendUnavailable) {
					t.Fatalf("construct = %v, %v", f, err)
				}
			})
		}
	}
}

func TestLinuxRecordFilesRejectsUnsafeRecords(t *testing.T) {
	f, lease := linuxFiles(t)
	key := RecordKey{f.key, digest("record", []byte("unsafe"))}
	path := filepath.Join(recordPaths(lease.Identity().GitCommonDir, f.key)[2], string(key.Record)[7:])
	for _, attack := range []struct {
		name  string
		setup func(t *testing.T, path string)
	}{
		{"symlink", func(t *testing.T, p string) {
			if err := os.Symlink(t.TempDir(), p); err != nil {
				t.Fatal(err)
			}
		}},
		{"directory", func(t *testing.T, p string) {
			if err := os.Mkdir(p, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{"fifo", func(t *testing.T, p string) {
			if err := unix.Mkfifo(p, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"socket", func(t *testing.T, p string) {
			shortFile, err := os.CreateTemp("/tmp", "dr-socket-")
			if err != nil {
				t.Fatal(err)
			}
			short := shortFile.Name()
			if err := shortFile.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(short); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Remove(short) })
			fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer unix.Close(fd)
			if err := unix.Bind(fd, &unix.SockaddrUnix{Name: short}); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(short, p); err != nil {
				t.Fatal(err)
			}
		}},
		{"wrong-mode", func(t *testing.T, p string) {
			if err := os.WriteFile(p, nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(attack.name, func(t *testing.T) {
			attack.setup(t, path)
			if _, err := f.Read(t.Context(), key); !errors.Is(err, ErrBackendUnavailable) {
				t.Fatalf("read: %v", err)
			} else if strings.Contains(err.Error(), filepath.Dir(lease.Identity().GitCommonDir)) {
				t.Fatalf("leaked filesystem path: %v", err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, key := range []RecordKey{{f.key, Digest("SHA256:" + string(digest("record", nil))[7:])}, {f.key, Digest("sha256:bad")}, {Digest("sha256:" + strings.Repeat("0", 64)), digest("record", nil)}} {
		if _, err := f.Read(t.Context(), key); !errors.Is(err, ErrIdentityChanged) {
			t.Fatalf("bad key: %v", err)
		}
	}
}

func TestLinuxRecordFilesRejectsLeaseDrift(t *testing.T) {
	f, lease := linuxFiles(t)
	key := RecordKey{f.key, digest("record", []byte("lease-drift"))}
	common := lease.Identity().GitCommonDir
	if err := os.Rename(common, common+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(common, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Read(t.Context(), key); !errors.Is(err, ErrIdentityChanged) {
		t.Fatalf("read after common-dir replacement: %v", err)
	}
	if err := f.Create(t.Context(), key, []byte("secret")); !errors.Is(err, ErrIdentityChanged) {
		t.Fatalf("create after common-dir replacement: %v", err)
	}
}

func TestLinuxRecordFilesReadStableSnapshot(t *testing.T) {
	f, lease := linuxFiles(t)
	key := RecordKey{f.key, digest("record", []byte("stable-snapshot"))}
	path := filepath.Join(recordPaths(lease.Identity().GitCommonDir, f.key)[2], string(key.Record)[7:])
	for _, test := range []struct {
		name   string
		mutate func(t *testing.T)
		want   string
		fail   bool
	}{
		{"unchanged", func(*testing.T) {}, "before", false},
		{"truncate", func(t *testing.T) {
			if err := os.Truncate(path, 0); err != nil {
				t.Fatal(err)
			}
		}, "", true},
		{"grow-overwrite", func(t *testing.T) {
			if err := os.WriteFile(path, append([]byte("after"), make([]byte, maxRecordBytes)...), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "", true},
		{"rename-replacement", func(t *testing.T) {
			if err := os.WriteFile(path+".next", []byte("replacement"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(path+".next", path); err != nil {
				t.Fatal(err)
			}
		}, "before", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte("before"), 0o600); err != nil {
				t.Fatal(err)
			}
			entered := make(chan struct{})
			proceed := make(chan struct{})
			f.afterFirstStat = func() { close(entered); <-proceed }
			result := make(chan struct {
				b   []byte
				err error
			}, 1)
			go func() {
				b, err := f.Read(t.Context(), key)
				result <- struct {
					b   []byte
					err error
				}{b, err}
			}()
			<-entered
			test.mutate(t)
			close(proceed)
			got := <-result
			if test.fail {
				if got.b != nil || !errors.Is(got.err, ErrBackendUnavailable) {
					t.Fatalf("read = %q, %v", got.b, got.err)
				}
				return
			}
			if test.name == "rename-replacement" && errors.Is(got.err, ErrBackendUnavailable) {
				return
			}
			if got.err != nil || string(got.b) != test.want {
				t.Fatalf("read = %q, %v", got.b, got.err)
			}
		})
	}
}

func TestLinuxRecordFilesReadHooksAreInstanceScoped(t *testing.T) {
	first, firstLease := linuxFiles(t)
	second, secondLease := linuxFiles(t)
	files := []struct {
		f     *linuxRecordFiles
		lease *reviewtransaction.RepositoryIdentityLease
		calls atomic.Int32
	}{
		{f: first, lease: firstLease}, {f: second, lease: secondLease},
	}
	results := make(chan error, len(files))
	for i := range files {
		file := &files[i]
		key := RecordKey{file.f.key, digest("record", []byte("instance-hook"))}
		path := filepath.Join(recordPaths(file.lease.Identity().GitCommonDir, file.f.key)[2], string(key.Record)[7:])
		if err := os.WriteFile(path, []byte("bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
		file.f.afterFirstStat = func() { file.calls.Add(1) }
		go func() { _, err := file.f.Read(t.Context(), key); results <- err }()
	}
	for range files {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	for i := range files {
		if got := files[i].calls.Load(); got != 1 {
			t.Fatalf("instance %d calls = %d", i, got)
		}
	}
}

func recordPaths(root string, key Digest) [3]string {
	first := filepath.Join(root, "gentle-ai")
	second := filepath.Join(first, "direct-run-records")
	return [3]string{first, second, filepath.Join(second, string(key)[7:])}
}

func adversarialLease(t *testing.T, repo string) *reviewtransaction.RepositoryIdentityLease {
	t.Helper()
	if out, err := exec.Command("git", "init", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	return lease
}
