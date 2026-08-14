//go:build linux || darwin

package directrun

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
	"golang.org/x/sys/unix"
)

func testOperationFiles(t *testing.T) (*linuxOperationFiles, string) {
	t.Helper()
	repo := t.TempDir()
	if output, err := exec.Command("git", "init", "--quiet", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	root := filepath.Join(repo, "editable")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return testOperationFilesAt(t, repo), repo
}

func testOperationFilesAt(t *testing.T, repo string) *linuxOperationFiles {
	t.Helper()
	root := filepath.Join(repo, "editable")
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := NewHandoff("run-3026-files", "worker-3026-files", []string{root}, "operate files", []string{"operate with retained authority"}, []Command{{Argv: []string{"go", "test", "./internal/directrun"}, CWD: repo}})
	if err != nil {
		t.Fatal(err)
	}
	files, err := newOperationFiles(context.Background(), lease, handoff)
	if err != nil {
		t.Fatal(err)
	}
	return files.(*linuxOperationFiles)
}

func TestOperationFilesReadEditAndTree(t *testing.T) {
	files, repo := testOperationFiles(t)
	defer files.Close()
	path := filepath.Join(repo, "editable", "bytes.bin")
	old := []byte{0, 1, 2, 3}
	if err := os.WriteFile(path, old, 0o640); err != nil {
		t.Fatal(err)
	}
	read, err := files.Read(context.Background(), "editable/bytes.bin", 1, 2)
	if err != nil || read.ContentB64 != "AQI=" || read.DataSHA256 != DigestSHA256(old) || read.TotalSize != 4 {
		t.Fatalf("read = %#v, %v", read, err)
	}
	edit, err := files.Edit(context.Background(), "editable/bytes.bin", DigestSHA256(old), []Replacement{{Start: 1, End: 3, Text: []byte("xy")}})
	if err != nil || !edit.Changed || edit.Publication != "published" || edit.ResultSHA256 != DigestSHA256([]byte{0, 'x', 'y', 3}) {
		t.Fatalf("edit = %#v, %v", edit, err)
	}
	tree, err := files.Tree(context.Background(), "editable")
	if err != nil || tree.Encoding != "utf-8" || tree.EvidenceSHA256 != DigestSHA256([]byte("f /bytes.bin")) {
		t.Fatalf("tree = %#v, %v", tree, err)
	}
}

func TestOperationFilesRejectEscapesAndConflicts(t *testing.T) {
	files, repo := testOperationFiles(t)
	defer files.Close()
	if err := os.WriteFile(filepath.Join(repo, "editable", "a"), []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, logical := range []string{"../editable/a", "editable/missing"} {
		_, err := files.Read(context.Background(), logical, 0, 1)
		if !errors.Is(err, ErrOperationInvalidPath) && !errors.Is(err, ErrOperationNotFound) {
			t.Errorf("%q: %v", logical, err)
		}
	}
	_, err := files.Edit(context.Background(), "editable/a", DigestSHA256([]byte("wrong")), nil)
	if !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("wrong base: %v", err)
	}
	_, err = files.Edit(context.Background(), "editable/a", DigestSHA256([]byte("abc")), []Replacement{{Start: 2, End: 4}})
	if !errors.Is(err, ErrOperationLimit) {
		t.Fatalf("out of bounds: %v", err)
	}
}

func TestOperationFilesEditFaultsPreserveOldBytes(t *testing.T) {
	for _, fault := range []struct {
		name string
		set  func(*linuxOperationFiles)
	}{
		{"fchown", func(f *linuxOperationFiles) { f.ops.fchown = func(int, int, int) error { return errors.New("fault") } }},
		{"fchmod", func(f *linuxOperationFiles) { f.ops.fchmod = func(int, uint32) error { return errors.New("fault") } }},
		{"file-sync", func(f *linuxOperationFiles) { f.ops.fsync = func(int) error { return errors.New("fault") } }},
		{"rename", func(f *linuxOperationFiles) {
			f.ops.rename = func(int, string, int, string) error { return errors.New("fault") }
		}},
	} {
		t.Run(fault.name, func(t *testing.T) {
			files, repo := testOperationFiles(t)
			defer files.Close()
			name := filepath.Join(repo, "editable", "a")
			if err := os.WriteFile(name, []byte("old"), 0o640); err != nil {
				t.Fatal(err)
			}
			fault.set(files)
			_, err := files.Edit(context.Background(), "editable/a", DigestSHA256([]byte("old")), []Replacement{{Start: 0, End: 3, Text: []byte("new")}})
			if err == nil {
				t.Fatal("accepted injected failure")
			}
			got, readErr := os.ReadFile(name)
			if readErr != nil || string(got) != "old" {
				t.Fatalf("got %q: %v", got, readErr)
			}
		})
	}
}

func TestOperationFilesDirectReadMatrix(t *testing.T) {
	files, repo := testOperationFiles(t)
	defer files.Close()
	name := filepath.Join(repo, "editable", "bytes")
	contents := []byte{0, 1, 2, 3}
	if err := os.WriteFile(name, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name          string
		offset, limit int64
		want          []byte
	}{
		{"empty", 0, 1, nil},
		{"complete", 0, 4, contents},
		{"beginning", 0, 2, contents[:2]},
		{"middle", 1, 2, contents[1:3]},
		{"to eof", 2, 8, contents[2:]},
		{"past eof", 9, 1, nil},
		{"max limit", 0, maxContent, contents},
	} {
		t.Run(tt.name, func(t *testing.T) {
			logical := "editable/bytes"
			if tt.name == "empty" {
				logical = "editable/empty"
				if err := os.WriteFile(filepath.Join(repo, logical), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := files.Read(t.Context(), logical, tt.offset, tt.limit)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := base64.StdEncoding.DecodeString(got.ContentB64)
			if err != nil || string(decoded) != string(tt.want) || got.DataSHA256 != DigestSHA256(map[bool][]byte{true: nil, false: contents}[tt.name == "empty"]) {
				t.Fatalf("result %#v decoded %x: %v", got, decoded, err)
			}
			wantOffset := tt.offset
			if wantOffset > int64(len(map[bool][]byte{true: nil, false: contents}[tt.name == "empty"])) {
				wantOffset = int64(len(map[bool][]byte{true: nil, false: contents}[tt.name == "empty"]))
			}
			if got.Offset != wantOffset || got.Truncated != (wantOffset != 0 || len(decoded) != int(got.TotalSize)) {
				t.Fatalf("metadata %#v", got)
			}
		})
	}
	if _, err := files.Read(t.Context(), "editable/bytes", 0, maxContent+1); !errors.Is(err, ErrOperationInvalidPath) {
		t.Fatalf("over-wire limit = %v", err)
	}
}

func TestOperationFilesDirectReadFaultsAndDrift(t *testing.T) {
	for _, tt := range []struct {
		name string
		set  func(*linuxOperationFiles, string)
	}{
		{"short reads", func(f *linuxOperationFiles, _ string) {
			read := f.ops.read
			f.ops.read = func(fd int, b []byte) (int, error) {
				if len(b) > 1 {
					b = b[:1]
				}
				return read(fd, b)
			}
		}},
		{"zero progress", func(f *linuxOperationFiles, _ string) { f.ops.read = func(int, []byte) (int, error) { return 0, nil } }},
		{"read error", func(f *linuxOperationFiles, _ string) {
			f.ops.read = func(int, []byte) (int, error) { return 0, unix.EIO }
		}},
		{"postread drift", func(f *linuxOperationFiles, _ string) { f.ops.postRead = func() error { return unix.ESTALE } }},
		{"file grow", func(f *linuxOperationFiles, name string) {
			f.ops.postRead = func() error { return os.WriteFile(name, []byte("older"), 0o600) }
		}},
		{"file shrink", func(f *linuxOperationFiles, name string) {
			f.ops.postRead = func() error { return os.WriteFile(name, []byte("o"), 0o600) }
		}},
		{"same size mutation", func(f *linuxOperationFiles, name string) {
			f.ops.postRead = func() error { return os.WriteFile(name, []byte("new"), 0o600) }
		}},
		{"target replacement", func(f *linuxOperationFiles, name string) {
			f.ops.postRead = func() error { return os.Rename(name+".next", name) }
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			files, repo := testOperationFiles(t)
			defer files.Close()
			name := filepath.Join(repo, "editable", "a")
			if err := os.WriteFile(name, []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			if tt.name == "target replacement" {
				if err := os.WriteFile(name+".next", []byte("new"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			tt.set(files, name)
			got, err := files.Read(t.Context(), "editable/a", 0, 3)
			if tt.name == "short reads" {
				if err != nil || got.DataSHA256 != DigestSHA256([]byte("old")) {
					t.Fatalf("short read = %#v, %v", got, err)
				}
				return
			}
			if !errors.Is(err, ErrOperationUnavailable) || got != (ReadResult{}) {
				t.Fatalf("failure returned %#v, %v", got, err)
			}
		})
	}
}

func TestOperationFilesDirectReadRejectsNamespaceAndLifecycle(t *testing.T) {
	files, repo := testOperationFiles(t)
	defer files.Close()
	root := filepath.Join(repo, "editable")
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file", filepath.Join(root, "target-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/dev/null", filepath.Join(root, "device-link")); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(root, "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	socket, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(socket)
	if err := unix.Bind(socket, &unix.SockaddrUnix{Name: filepath.Join(root, "socket")}); err != nil {
		t.Fatal(err)
	}
	for _, logical := range []string{"editable/link/file", "editable/target-link", "editable/device-link", "editable/fifo", "editable/socket", "editable", "editable/missing"} {
		if _, err := files.Read(t.Context(), logical, 0, 1); err == nil {
			t.Errorf("accepted %s", logical)
		}
	}
	if _, err := (*linuxOperationFiles)(nil).Read(t.Context(), "a", 0, 1); !errors.Is(err, ErrOperationUnavailable) {
		t.Fatal(err)
	}
	if _, err := (&linuxOperationFiles{}).Read(t.Context(), "a", 0, 1); !errors.Is(err, ErrOperationUnavailable) {
		t.Fatal(err)
	}
	if err := files.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := files.Read(t.Context(), "editable/file", 0, 1); !errors.Is(err, ErrOperationUnavailable) {
		t.Fatal(err)
	}
}

func TestOperationFilesDirectReadAuthorityIsolationAndClose(t *testing.T) {
	first, repo := testOperationFiles(t)
	defer first.Close()
	second, secondRepo := testOperationFiles(t)
	defer second.Close()
	if err := os.WriteFile(filepath.Join(repo, "editable", "a"), []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secondRepo, "editable", "a"), []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	first.ops.read = func(int, []byte) (int, error) { return 0, unix.EIO }
	if _, err := first.Read(t.Context(), "editable/a", 0, 3); !errors.Is(err, ErrOperationUnavailable) {
		t.Fatal(err)
	}
	if _, err := second.Read(t.Context(), "editable/a", 0, 3); err != nil {
		t.Fatalf("isolated authority: %v", err)
	}
	ready, release := make(chan struct{}), make(chan struct{})
	read := second.ops.read
	second.ops.read = func(fd int, b []byte) (int, error) {
		select {
		case ready <- struct{}{}:
			<-release
		default:
		}
		return read(fd, b)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = second.Read(context.Background(), "editable/a", 0, 3) }()
	<-ready
	go func() { defer wg.Done(); _ = second.Close() }()
	close(release)
	wg.Wait()
}

func TestOperationFilesDirectReadFailureClosesAndRedacts(t *testing.T) {
	files, repo := testOperationFiles(t)
	defer files.Close()
	if err := os.WriteFile(filepath.Join(repo, "editable", "a"), []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	closed := 0
	closeFD := files.ops.close
	files.ops.close = func(fd int) error { closed++; return closeFD(fd) }
	files.ops.read = func(int, []byte) (int, error) {
		return 0, fmt.Errorf("root=/private HOME=/home/user path=editable/a digest=%s errno=5 0x5", DigestSHA256([]byte("abc")))
	}
	_, err := files.Read(t.Context(), "editable/a", 0, 3)
	if !errors.Is(err, ErrOperationUnavailable) || closed != 3 {
		t.Fatalf("read failure = %v, closes = %d", err, closed)
	}
	for _, token := range []string{"/private", "/home/user", "editable/a", "errno=5", "0x5", DigestSHA256([]byte("abc"))} {
		if strings.Contains(err.Error(), token) {
			t.Fatalf("public error leaked %q: %v", token, err)
		}
	}
}

func TestOperationFilesEditNoopAndOrderedReplacements(t *testing.T) {
	files, repo := testOperationFiles(t)
	defer files.Close()
	name := filepath.Join(repo, "editable", "a")
	if err := os.WriteFile(name, []byte("abcdef"), 0o640); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	noop, err := files.Edit(t.Context(), "editable/a", DigestSHA256([]byte("abcdef")), nil)
	if err != nil || noop.Changed || noop.Publication != "unchanged" {
		t.Fatalf("noop = %#v, %v", noop, err)
	}
	after, err := os.Stat(name)
	if err != nil || !os.SameFile(before, after) || after.Mode() != before.Mode() {
		t.Fatalf("noop changed file: %v", err)
	}
	got, err := files.Edit(t.Context(), "editable/a", DigestSHA256([]byte("abcdef")), []Replacement{{Start: 1, End: 3, Text: []byte("X")}, {Start: 4, End: 6, Text: []byte("YZ")}})
	if err != nil || !got.Changed || got.Publication != "published" || got.ResultSHA256 != DigestSHA256([]byte("aXdYZ")) {
		t.Fatalf("edit = %#v, %v", got, err)
	}
	bytes, err := os.ReadFile(name)
	if err != nil || string(bytes) != "aXdYZ" {
		t.Fatalf("bytes = %q, %v", bytes, err)
	}
}

func TestOperationFilesEditFaultsCleanCandidate(t *testing.T) {
	for _, tt := range []struct {
		name string
		set  func(*linuxOperationFiles)
	}{
		{"temp", func(f *linuxOperationFiles) {
			f.ops.temp = func(int, uint32, uint32, uint32) (string, int, error) { return "", -1, unix.EIO }
		}},
		{"write zero", func(f *linuxOperationFiles) { f.ops.write = func(int, []byte) (int, error) { return 0, nil } }},
		{"write error", func(f *linuxOperationFiles) { f.ops.write = func(int, []byte) (int, error) { return 0, unix.EIO } }},
		{"file close", func(f *linuxOperationFiles) {
			closeFD := f.ops.close
			f.ops.close = func(fd int) error { _ = closeFD(fd); return unix.EIO }
		}},
		{"rename", func(f *linuxOperationFiles) { f.ops.rename = func(int, string, int, string) error { return unix.EIO } }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			files, repo := testOperationFiles(t)
			defer files.Close()
			name := filepath.Join(repo, "editable", "a")
			if err := os.WriteFile(name, []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			tt.set(files)
			_, err := files.Edit(t.Context(), "editable/a", DigestSHA256([]byte("old")), []Replacement{{Start: 0, End: 3, Text: []byte("new")}})
			if err == nil {
				t.Fatal("accepted fault")
			}
			bytes, readErr := os.ReadFile(name)
			if readErr != nil || string(bytes) != "old" {
				t.Fatalf("bytes = %q, %v", bytes, readErr)
			}
			entries, readErr := os.ReadDir(filepath.Join(repo, "editable"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".direct-") {
					t.Fatalf("residue %q", entry.Name())
				}
			}
		})
	}
}

func TestOperationFilesEditPrepublishReopensDestination(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(string) error
	}{
		{"same size", func(name string) error { return os.WriteFile(name, []byte("bad"), 0o600) }},
		{"grow", func(name string) error { return os.WriteFile(name, []byte("older"), 0o600) }},
		{"shrink", func(name string) error { return os.WriteFile(name, []byte("o"), 0o600) }},
		{"swap", func(name string) error { return os.Rename(name+".next", name) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			files, repo := testOperationFiles(t)
			defer files.Close()
			name := filepath.Join(repo, "editable", "a")
			if err := os.WriteFile(name, []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			if tt.name == "swap" {
				if err := os.WriteFile(name+".next", []byte("other"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			statAt := files.ops.fstatAt
			mutated := false
			files.ops.fstatAt = func(parent int, path string, stat *unix.Stat_t, flags int) error {
				err := statAt(parent, path, stat, flags)
				if err == nil && path == "a" && !mutated {
					mutated = true
					return tt.mutate(name)
				}
				return err
			}
			got, err := files.Edit(t.Context(), "editable/a", DigestSHA256([]byte("old")), []Replacement{{Start: 0, End: 3, Text: []byte("new")}})
			if !errors.Is(err, ErrOperationConflict) || got != (EditResult{}) {
				t.Fatalf("result = %#v, %v", got, err)
			}
			bytes, readErr := os.ReadFile(name)
			if readErr != nil || string(bytes) == "new" {
				t.Fatalf("external bytes = %q, %v", bytes, readErr)
			}
		})
	}
}

func TestOperationFilesEditReportsUnknownAfterRenameAndUnlockFailures(t *testing.T) {
	for _, tt := range []struct {
		name string
		set  func(*linuxOperationFiles)
	}{
		{"directory sync", func(f *linuxOperationFiles) {
			sync := f.ops.fsync
			calls := 0
			f.ops.fsync = func(fd int) error {
				calls++
				if calls == 2 {
					return unix.EIO
				}
				return sync(fd)
			}
		}},
		{"unlock", func(f *linuxOperationFiles) {
			flock := f.ops.flock
			f.ops.flock = func(fd, operation int) error {
				if operation == unix.LOCK_UN {
					return unix.EIO
				}
				return flock(fd, operation)
			}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			files, repo := testOperationFiles(t)
			defer files.Close()
			name := filepath.Join(repo, "editable", "a")
			if err := os.WriteFile(name, []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			tt.set(files)
			got, err := files.Edit(t.Context(), "editable/a", DigestSHA256([]byte("old")), []Replacement{{Start: 0, End: 3, Text: []byte("new")}})
			if !errors.Is(err, ErrOperationPublication) || got != (EditResult{}) {
				t.Fatalf("result = %#v, %v", got, err)
			}
			bytes, readErr := os.ReadFile(name)
			if readErr != nil || string(bytes) != "new" {
				t.Fatalf("published bytes = %q, %v", bytes, readErr)
			}
		})
	}
}

func TestOperationFilesTreeCanonicalEvidence(t *testing.T) {
	files, repo := testOperationFiles(t)
	defer files.Close()
	root := filepath.Join(repo, "editable")
	if err := os.WriteFile(filepath.Join(root, "z"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dir", "a"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("dir", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	got, err := files.Tree(t.Context(), "editable")
	want := []byte("d /dir\nf /dir/a\nl /link\nf /z")
	if err != nil || got.Encoding != "utf-8" || got.Truncated || got.EvidenceSHA256 != DigestSHA256(want) || got.ContentB64 != base64.StdEncoding.EncodeToString(want) {
		t.Fatalf("tree = %#v, %v", got, err)
	}
	again, err := files.Tree(t.Context(), "editable")
	if err != nil || again != got {
		t.Fatalf("unstable tree = %#v, %v", again, err)
	}
	subtree, err := files.Tree(t.Context(), "editable/dir")
	if err != nil || subtree.ContentB64 != base64.StdEncoding.EncodeToString([]byte("f /a")) {
		t.Fatalf("subtree = %#v, %v", subtree, err)
	}
	if _, err := files.Tree(t.Context(), "editable/link"); !errors.Is(err, ErrOperationUnavailable) {
		t.Fatalf("symlink component = %v", err)
	}
}

func TestOperationFilesTreeRejectsSpecialAndMutation(t *testing.T) {
	files, repo := testOperationFiles(t)
	defer files.Close()
	root := filepath.Join(repo, "editable")
	if err := unix.Mkfifo(filepath.Join(root, "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := files.Tree(t.Context(), "editable"); !errors.Is(err, ErrOperationUnavailable) {
		t.Fatalf("fifo = %v", err)
	}
	if err := os.Remove(filepath.Join(root, "fifo")); err != nil {
		t.Fatal(err)
	}
	socket, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(socket)
	if err := unix.Bind(socket, &unix.SockaddrUnix{Name: filepath.Join(root, "socket")}); err != nil {
		t.Fatal(err)
	}
	if _, err := files.Tree(t.Context(), "editable"); !errors.Is(err, ErrOperationUnavailable) {
		t.Fatalf("socket = %v", err)
	}
	if err := os.Remove(filepath.Join(root, "socket")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	readDir := files.ops.readDir
	mutated := false
	files.ops.readDir = func(fd int, data []byte) (int, error) {
		n, err := readDir(fd, data)
		if n > 0 && !mutated {
			mutated = true
			if writeErr := os.WriteFile(filepath.Join(root, "b"), []byte("b"), 0o600); writeErr != nil {
				return 0, writeErr
			}
		}
		return n, err
	}
	got, err := files.Tree(t.Context(), "editable")
	if !errors.Is(err, ErrOperationConflict) || got != (InspectResult{}) {
		t.Fatalf("mutation = %#v, %v", got, err)
	}
}

func TestOperationFilesTreeDepthCap(t *testing.T) {
	files, repo := testOperationFiles(t)
	defer files.Close()
	path := filepath.Join(repo, "editable")
	for range maxTreeDepth + 1 {
		path = filepath.Join(path, "d")
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got, err := files.Tree(t.Context(), "editable")
	if !errors.Is(err, ErrOperationLimit) || got != (InspectResult{}) {
		t.Fatalf("depth = %#v, %v", got, err)
	}
}

func TestOperationFilesTreeLifecycleFaultsAndRedaction(t *testing.T) {
	files, repo := testOperationFiles(t)
	defer files.Close()
	if err := os.WriteFile(filepath.Join(repo, "editable", "a"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		set  func(*linuxOperationFiles)
	}{
		{"seek", func(f *linuxOperationFiles) { f.ops.seek = func(int, int64, int) (int64, error) { return 0, unix.EIO } }},
		{"read-dir", func(f *linuxOperationFiles) {
			f.ops.readDir = func(int, []byte) (int, error) {
				return 0, fmt.Errorf("root=/private HOME=/home path=editable errno=5 0x5")
			}
		}},
		{"stat", func(f *linuxOperationFiles) { f.ops.fstat = func(int, *unix.Stat_t) error { return unix.EIO } }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f, r := testOperationFiles(t)
			defer f.Close()
			if err := os.WriteFile(filepath.Join(r, "editable", "a"), []byte("a"), 0o600); err != nil {
				t.Fatal(err)
			}
			tt.set(f)
			got, err := f.Tree(t.Context(), "editable")
			if !errors.Is(err, ErrOperationUnavailable) || got != (InspectResult{}) {
				t.Fatalf("result = %#v, %v", got, err)
			}
			if strings.Contains(err.Error(), "/private") || strings.Contains(err.Error(), "errno=5") {
				t.Fatalf("leaked %v", err)
			}
		})
	}
	if _, err := (*linuxOperationFiles)(nil).Tree(t.Context(), "editable"); !errors.Is(err, ErrOperationUnavailable) {
		t.Fatal(err)
	}
	if _, err := (&linuxOperationFiles{}).Tree(t.Context(), "editable"); !errors.Is(err, ErrOperationUnavailable) {
		t.Fatal(err)
	}
	if err := files.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := files.Tree(t.Context(), "editable"); !errors.Is(err, ErrOperationUnavailable) {
		t.Fatal(err)
	}
}

func TestOperationFilesEditIndependentAuthoritiesConflict(t *testing.T) {
	first, repo := testOperationFiles(t)
	defer first.Close()
	second := testOperationFilesAt(t, repo)
	defer second.Close()
	name := filepath.Join(repo, "editable", "a")
	if err := os.WriteFile(name, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	locked, release := make(chan struct{}), make(chan struct{})
	flock := first.ops.flock
	first.ops.flock = func(fd, operation int) error {
		err := flock(fd, operation)
		if err == nil && operation&unix.LOCK_UN == 0 {
			locked <- struct{}{}
			<-release
		}
		return err
	}
	type outcome struct {
		result EditResult
		err    error
	}
	firstResult, secondResult := make(chan outcome, 1), make(chan outcome, 1)
	go func() {
		result, err := first.Edit(context.Background(), "editable/a", DigestSHA256([]byte("old")), []Replacement{{Start: 0, End: 3, Text: []byte("one")}})
		firstResult <- outcome{result, err}
	}()
	<-locked
	go func() {
		result, err := second.Edit(context.Background(), "editable/a", DigestSHA256([]byte("old")), []Replacement{{Start: 0, End: 3, Text: []byte("two")}})
		secondResult <- outcome{result, err}
	}()
	close(release)
	a, b := <-firstResult, <-secondResult
	if a.err != nil || !a.result.Changed || !errors.Is(b.err, ErrOperationConflict) || b.result != (EditResult{}) {
		t.Fatalf("outcomes = %#v %#v", a, b)
	}
	bytes, err := os.ReadFile(name)
	if err != nil || string(bytes) != "one" {
		t.Fatalf("winner bytes = %q, %v", bytes, err)
	}
}
