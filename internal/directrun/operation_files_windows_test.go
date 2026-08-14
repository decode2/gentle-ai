//go:build windows

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
	"golang.org/x/sys/windows"
)

func TestWindowsOperationFilesRejectInvalidAuthority(t *testing.T) {
	files, err := newPlatformOperationFiles(context.Background(), nil, Handoff{})
	if files != nil || !errors.Is(err, ErrOperationUnavailable) {
		t.Fatalf("files=%v err=%v", files, err)
	}
}

func TestWindowsOperationFilesReadEditAndTree(t *testing.T) {
	repo := t.TempDir()
	if output, err := exec.Command("git", "init", "--quiet", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	editable := filepath.Join(repo, "editable")
	if err := os.Mkdir(editable, 0o755); err != nil {
		t.Fatal(err)
	}
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := NewHandoff("windows-files", "windows-worker", []string{editable}, "operate files", []string{"handle relative"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	files, err := newOperationFiles(t.Context(), lease, handoff)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	if err := os.WriteFile(filepath.Join(editable, "a"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	read, err := files.Read(t.Context(), "editable/a", 1, 2)
	if err != nil || read.TotalSize != 3 || read.ContentB64 != "bGQ=" {
		t.Fatalf("read=%#v err=%v", read, err)
	}
	edit, err := files.Edit(t.Context(), "editable/a", DigestSHA256([]byte("old")), []Replacement{{Start: 0, End: 3, Text: []byte("new")}})
	if err != nil || !edit.Changed || edit.Publication != "published" {
		t.Fatalf("edit=%#v err=%v", edit, err)
	}
	tree, err := files.Tree(t.Context(), "editable")
	if err != nil || tree.Encoding != "utf-8" || tree.ContentB64 != "ZiAvYQ==" {
		t.Fatalf("tree=%#v err=%v", tree, err)
	}
}

func TestWindowsLogicalPathsRejectWindowsAmbiguity(t *testing.T) {
	for _, value := range []string{"a:b", `a\\b`, "a/ ", "a/COM1", "a/NUL", "a/file."} {
		if windowsLogicalPath(value) {
			t.Errorf("accepted %q", value)
		}
	}
}

func TestWindowsTreeNamesRejectAmbiguousAndSpecialEntries(t *testing.T) {
	for _, value := range []string{"", "x/y", "x:y", "x\x00y", "x\r", "x\n", "NUL", "COM1", "file.", " name"} {
		if windowsName(value) {
			t.Errorf("accepted %q", value)
		}
	}
	for _, value := range []string{"a", "file.txt", "a-b_1"} {
		if !windowsName(value) {
			t.Errorf("rejected %q", value)
		}
	}
}

func TestWindowsReplacementFailureMatrix(t *testing.T) {
	old := []byte("abcdef")
	for _, tt := range []struct {
		name   string
		values []Replacement
		want   string
		err    error
	}{
		{"ordered", []Replacement{{Start: 1, End: 3, Text: []byte("X")}, {Start: 4, End: 6, Text: []byte("YZ")}}, "aXdYZ", nil},
		{"overlap", []Replacement{{Start: 1, End: 3}, {Start: 2, End: 4}}, "", ErrOperationConflict},
		{"outside", []Replacement{{Start: 0, End: 7}}, "", ErrOperationConflict},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := windowsApplyReplacements(old, tt.values)
			if !errors.Is(err, tt.err) || string(got) != tt.want {
				t.Fatalf("got=%q err=%v", got, err)
			}
		})
	}
}

func TestWindowsOperationFilesReadBoundariesAndPathRefusals(t *testing.T) {
	repo := t.TempDir()
	if output, err := exec.Command("git", "init", "--quiet", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	editable := filepath.Join(repo, "editable")
	if err := os.Mkdir(editable, 0o755); err != nil {
		t.Fatal(err)
	}
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := NewHandoff("windows-boundaries", "windows-worker", []string{editable}, "operate files", []string{"handle relative"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	value := []byte{0, 1, 2, 3}
	if err := os.WriteFile(filepath.Join(editable, "bytes"), value, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(editable, "empty"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := newOperationFiles(t.Context(), lease, handoff)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	for _, tt := range []struct {
		name          string
		path          string
		offset, limit int64
		want          []byte
	}{
		{"empty", "editable/empty", 0, 1, nil},
		{"binary", "editable/bytes", 0, 4, value},
		{"beginning", "editable/bytes", 0, 2, value[:2]},
		{"middle", "editable/bytes", 1, 2, value[1:3]},
		{"to-eof", "editable/bytes", 2, 8, value[2:]},
		{"eof", "editable/bytes", 9, 1, nil},
		{"max", "editable/bytes", 0, maxContent, value},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := files.Read(t.Context(), tt.path, tt.offset, tt.limit)
			decoded, decodeErr := base64.StdEncoding.DecodeString(got.ContentB64)
			wantOffset := tt.offset
			if wantOffset > int64(len(value)) || tt.name == "empty" {
				wantOffset = int64(len(tt.want))
			}
			full := value
			if tt.name == "empty" {
				full = nil
			}
			if err != nil || decodeErr != nil || string(decoded) != string(tt.want) || got.DataSHA256 != DigestSHA256(full) || got.TotalSize != int64(len(full)) || got.Offset != wantOffset || got.Truncated != (wantOffset != 0 || len(decoded) != len(full)) {
				t.Fatalf("read=%#v err=%v decode=%v", got, err, decodeErr)
			}
		})
	}
	for _, path := range []string{"../editable/bytes", "editable/../bytes", "editable/bytes:stream", `editable\\bytes`, "\\server\\share\\x", "//server/share/x", "/absolute", "C:/absolute", "editable/CON", "editable/con.txt", "editable/COM1", "editable/LPT9", "editable/file.", "editable/file ", "editable/ ", "editable//bytes"} {
		if _, err := files.Read(t.Context(), path, 0, 1); !errors.Is(err, ErrOperationInvalidPath) {
			t.Errorf("%q: %v", path, err)
		}
	}
}

func TestWindowsOperationFilesReadFaultsDriftAndLifecycle(t *testing.T) {
	for _, tt := range []struct {
		name string
		set  func(*windowsOperationFiles, string)
		ok   bool
	}{
		{"short-progress", func(f *windowsOperationFiles, _ string) {
			read := f.ops.read
			f.ops.read = func(h windows.Handle, data []byte, done *uint32) error {
				if len(data) > 1 {
					data = data[:1]
				}
				return read(h, data, done)
			}
		}, true},
		{"zero-progress", func(f *windowsOperationFiles, _ string) {
			f.ops.read = func(windows.Handle, []byte, *uint32) error { return nil }
		}, false},
		{"read-error", func(f *windowsOperationFiles, _ string) {
			f.ops.read = func(windows.Handle, []byte, *uint32) error { return windows.ERROR_READ_FAULT }
		}, false},
		{"grow", func(f *windowsOperationFiles, name string) {
			f.ops.afterRead = func() error { return os.WriteFile(name, []byte("older"), 0o600) }
		}, false},
		{"shrink", func(f *windowsOperationFiles, name string) {
			f.ops.afterRead = func() error { return os.WriteFile(name, []byte("o"), 0o600) }
		}, false},
		{"same-size-mutation", func(f *windowsOperationFiles, name string) {
			f.ops.afterRead = func() error { return os.WriteFile(name, []byte("new"), 0o600) }
		}, false},
		{"replacement", func(f *windowsOperationFiles, name string) {
			f.ops.afterRead = func() error { return os.Rename(name+".next", name) }
		}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			files, repo := testWindowsOperationFiles(t)
			defer files.Close()
			name := filepath.Join(repo, "editable", "a")
			if err := os.WriteFile(name, []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			if tt.name == "replacement" {
				if err := os.WriteFile(name+".next", []byte("new"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			tt.set(files, name)
			result, err := files.Read(t.Context(), "editable/a", 0, 3)
			if tt.ok {
				if err != nil || result.DataSHA256 != DigestSHA256([]byte("old")) {
					t.Fatalf("result=%#v err=%v", result, err)
				}
			} else if !errors.Is(err, ErrOperationUnavailable) || result != (ReadResult{}) {
				t.Fatalf("result=%#v err=%v", result, err)
			}
		})
	}
}

func TestWindowsOperationFilesReadRefusesDirectoryMissingAndReparse(t *testing.T) {
	files, repo := testWindowsOperationFiles(t)
	defer files.Close()
	root := filepath.Join(repo, "editable")
	if err := os.Mkdir(filepath.Join(root, "directory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := files.Read(t.Context(), "editable/directory", 0, 1); !errors.Is(err, ErrOperationUnavailable) {
		t.Fatalf("directory=%v", err)
	}
	if _, err := files.Read(t.Context(), "editable/missing", 0, 1); !errors.Is(err, ErrOperationNotFound) {
		t.Fatalf("missing=%v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "target"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(root, "link")); err != nil {
		t.Skipf("Windows reparse fixture unavailable: %v", err)
	}
	if _, err := files.Read(t.Context(), "editable/link", 0, 1); !errors.Is(err, ErrOperationUnavailable) {
		t.Fatalf("reparse target=%v", err)
	}
}

func TestWindowsOperationFilesReadCloseWaitsAndRedacts(t *testing.T) {
	files, repo := testWindowsOperationFiles(t)
	name := filepath.Join(repo, "editable", "a")
	if err := os.WriteFile(name, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	started, release, closed := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	read := files.ops.read
	files.ops.read = func(h windows.Handle, data []byte, done *uint32) error {
		select {
		case <-started:
		default:
			close(started)
			<-release
		}
		return read(h, data, done)
	}
	go func() { _, _ = files.Read(context.Background(), "editable/a", 0, 3) }()
	<-started
	go func() { closed <- files.Close() }()
	select {
	case <-closed:
		t.Fatal("Close returned during read")
	default:
	}
	close(release)
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if _, err := files.Read(t.Context(), "editable/a", 0, 1); !errors.Is(err, ErrOperationUnavailable) {
		t.Fatalf("closed read=%v", err)
	}

	files, repo = testWindowsOperationFiles(t)
	defer files.Close()
	if err := os.WriteFile(filepath.Join(repo, "editable", "a"), []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	closes := 0
	closeHandle := files.ops.close
	files.ops.close = func(h windows.Handle) error { closes++; return closeHandle(h) }
	files.ops.read = func(windows.Handle, []byte, *uint32) error {
		return fmt.Errorf("root=%s HOME=%s logical=editable/a digest=%s win32=5 0x5 nt=3221225473 0xc0000001", repo, os.Getenv("HOME"), DigestSHA256([]byte("abc")))
	}
	_, err := files.Read(t.Context(), "editable/a", 0, 3)
	if !errors.Is(err, ErrOperationUnavailable) || err != ErrOperationUnavailable || closes != 3 {
		t.Fatalf("error=%v closes=%d", err, closes)
	}
	for _, secret := range []string{repo, os.Getenv("HOME"), "editable/a", DigestSHA256([]byte("abc")), "win32=5", "0x5", "3221225473", "0xc0000001"} {
		if secret != "" && strings.Contains(err.Error(), secret) {
			t.Fatalf("leaked %q: %v", secret, err)
		}
	}
}

func TestWindowsOperationFilesReadAuthoritiesAreIsolated(t *testing.T) {
	first, repo := testWindowsOperationFiles(t)
	defer first.Close()
	second, secondRepo := testWindowsOperationFiles(t)
	defer second.Close()
	if err := os.WriteFile(filepath.Join(repo, "editable", "a"), []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secondRepo, "editable", "a"), []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	first.ops.read = func(windows.Handle, []byte, *uint32) error { return windows.ERROR_READ_FAULT }
	if _, err := first.Read(t.Context(), "editable/a", 0, 3); !errors.Is(err, ErrOperationUnavailable) {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := second.Read(t.Context(), "editable/a", 0, 3); results <- err }()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("isolated read: %v", err)
		}
	}
}

func testWindowsOperationFiles(t *testing.T) (*windowsOperationFiles, string) {
	t.Helper()
	repo := t.TempDir()
	if output, err := exec.Command("git", "init", "--quiet", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	editable := filepath.Join(repo, "editable")
	if err := os.Mkdir(editable, 0o755); err != nil {
		t.Fatal(err)
	}
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := NewHandoff("windows-read", "windows-worker", []string{editable}, "operate files", []string{"handle relative"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	files, err := newOperationFiles(t.Context(), lease, handoff)
	if err != nil {
		t.Fatal(err)
	}
	return files.(*windowsOperationFiles), repo
}

func TestWindowsOperationTempNamesArePrivateAndDistinct(t *testing.T) {
	first, err := windowsOperationTempName()
	if err != nil {
		t.Fatal(err)
	}
	second, err := windowsOperationTempName()
	if err != nil || first == second || !windowsName(first) || !windowsName(second) {
		t.Fatalf("names %q %q: %v", first, second, err)
	}
}
