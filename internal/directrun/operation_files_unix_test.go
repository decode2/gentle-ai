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
	return files.(*linuxOperationFiles), repo
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
