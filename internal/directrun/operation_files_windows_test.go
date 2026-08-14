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

func TestWindowsOperationFilesEditNoopAndReplacementMatrix(t *testing.T) {
	files, repo := testWindowsOperationFiles(t)
	defer files.Close()
	name := filepath.Join(repo, "editable", "a")
	old := []byte("abcdef")
	if err := os.WriteFile(name, old, 0o600); err != nil {
		t.Fatal(err)
	}
	temps, publishes := 0, 0
	tempName, publish := files.ops.tempName, files.ops.publish
	files.ops.tempName = func() (string, error) { temps++; return tempName() }
	files.ops.publish = func(source, parent windows.Handle, target string, proof windowsDestinationProof) error {
		publishes++
		return publish(source, parent, target, proof)
	}
	before := editProof(t, files, "editable/a")
	result, err := files.Edit(t.Context(), "editable/a", DigestSHA256(old), nil)
	after := editProof(t, files, "editable/a")
	if err != nil || result != (EditResult{ResultSHA256: DigestSHA256(old), Publication: "unchanged"}) || temps != 0 || publishes != 0 || before.id != after.id || before.owner != after.owner || before.protected != after.protected || string(before.dacl) != string(after.dacl) || before.attrs != after.attrs {
		t.Fatalf("noop=%#v err=%v temp=%d publish=%d", result, err, temps, publishes)
	}
	for _, tt := range []struct {
		name         string
		base         string
		replacements []Replacement
		want         []byte
		wantErr      error
	}{
		{"empty", DigestSHA256(old), []Replacement{{Start: 0, End: 6}}, nil, nil},
		{"small", DigestSHA256(old), []Replacement{{Start: 1, End: 3, Text: []byte("X")}}, []byte("aXdef"), nil},
		{"multiple", DigestSHA256(old), []Replacement{{Start: 1, End: 3, Text: []byte("X")}, {Start: 4, End: 6, Text: []byte("YZ")}}, []byte("aXdYZ"), nil},
		{"wrong-base", DigestSHA256([]byte("wrong")), nil, nil, ErrOperationConflict},
		{"overlap", DigestSHA256(old), []Replacement{{Start: 1, End: 3}, {Start: 2, End: 4}}, nil, ErrOperationConflict},
		{"unsorted", DigestSHA256(old), []Replacement{{Start: 4, End: 5}, {Start: 1, End: 2}}, nil, ErrOperationConflict},
		{"out-of-bounds", DigestSHA256(old), []Replacement{{Start: 0, End: 7}}, nil, ErrOperationLimit},
		{"oversize", DigestSHA256(old), []Replacement{{Start: 0, End: 0, Text: make([]byte, maxOperationFileBytes+1)}}, nil, ErrOperationLimit},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(name, old, 0o600); err != nil {
				t.Fatal(err)
			}
			before := editProof(t, files, "editable/a")
			got, err := files.Edit(t.Context(), "editable/a", tt.base, tt.replacements)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) || got != (EditResult{}) {
					t.Fatalf("result=%#v err=%v", got, err)
				}
				return
			}
			if err != nil || !got.Changed || got.Publication != "published" || got.ResultSHA256 != DigestSHA256(tt.want) {
				t.Fatalf("result=%#v err=%v", got, err)
			}
			after := editProof(t, files, "editable/a")
			if after.owner != before.owner || after.protected != before.protected || string(after.dacl) != string(before.dacl) || after.attrs != before.attrs {
				t.Fatalf("successor proof changed: before=%#v after=%#v", before, after)
			}
			bytes, readErr := os.ReadFile(name)
			if readErr != nil || string(bytes) != string(tt.want) {
				t.Fatalf("bytes=%q err=%v", bytes, readErr)
			}
		})
	}
}

func TestWindowsOperationFilesEditFaultsCleanCandidateExactlyOnce(t *testing.T) {
	for _, tt := range []struct {
		name string
		set  func(*windowsOperationFiles)
	}{
		{"create", func(f *windowsOperationFiles) {
			f.ops.create = func(windows.Handle, string) (windows.Handle, error) { return 0, windows.ERROR_ACCESS_DENIED }
		}},
		{"write-zero", func(f *windowsOperationFiles) {
			f.ops.write = func(windows.Handle, []byte, *uint32) error { return nil }
		}},
		{"write-error", func(f *windowsOperationFiles) {
			f.ops.write = func(windows.Handle, []byte, *uint32) error { return windows.ERROR_WRITE_FAULT }
		}},
		{"flush", func(f *windowsOperationFiles) {
			f.ops.flush = func(windows.Handle) error { return windows.ERROR_WRITE_FAULT }
		}},
		{"publish", func(f *windowsOperationFiles) {
			f.ops.publish = func(windows.Handle, windows.Handle, string, windowsDestinationProof) error {
				return windows.ERROR_ACCESS_DENIED
			}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			files, repo := testWindowsOperationFiles(t)
			defer files.Close()
			name := filepath.Join(repo, "editable", "a")
			if err := os.WriteFile(name, []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			candidate, closes, cleanups := windows.Handle(0), 0, 0
			create, closeHandle, cleanup := files.ops.create, files.ops.close, files.ops.cleanup
			files.ops.create = func(parent windows.Handle, temp string) (windows.Handle, error) {
				h, err := create(parent, temp)
				candidate = h
				return h, err
			}
			files.ops.close = func(h windows.Handle) error {
				if h == candidate && h != 0 {
					closes++
				}
				return closeHandle(h)
			}
			files.ops.cleanup = func(h windows.Handle) error {
				if h == candidate {
					cleanups++
				}
				return cleanup(h)
			}
			tt.set(files)
			result, err := files.Edit(t.Context(), "editable/a", DigestSHA256([]byte("old")), []Replacement{{Start: 0, End: 3, Text: []byte("new")}})
			if !errors.Is(err, ErrOperationUnavailable) || result != (EditResult{}) {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			bytes, readErr := os.ReadFile(name)
			if readErr != nil || string(bytes) != "old" {
				t.Fatalf("bytes=%q err=%v", bytes, readErr)
			}
			if candidate != 0 && (closes != 1 || cleanups != 1) {
				t.Fatalf("candidate closes=%d cleanups=%d", closes, cleanups)
			}
		})
	}
}

func TestWindowsOperationFilesEditPublicationFailuresAreExplicit(t *testing.T) {
	for _, tt := range []struct {
		name string
		set  func(*windowsOperationFiles)
		want error
	}{
		{"unknown-publish", func(f *windowsOperationFiles) {
			f.ops.publish = func(windows.Handle, windows.Handle, string, windowsDestinationProof) error {
				return errWindowsPublicationUnknown
			}
		}, ErrOperationPublication},
		{"source-close-after-publish", func(f *windowsOperationFiles) {
			lock := f.ops.lock
			var source windows.Handle
			f.ops.lock = func(h windows.Handle, flags, reserved, low, high uint32, over *windows.Overlapped) error {
				source = h
				return lock(h, flags, reserved, low, high, over)
			}
			closeHandle := f.ops.close
			f.ops.close = func(h windows.Handle) error {
				err := closeHandle(h)
				if h == source {
					return windows.ERROR_INVALID_HANDLE
				}
				return err
			}
		}, ErrOperationPublication},
	} {
		t.Run(tt.name, func(t *testing.T) {
			files, repo := testWindowsOperationFiles(t)
			defer files.Close()
			name := filepath.Join(repo, "editable", "a")
			if err := os.WriteFile(name, []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			tt.set(files)
			result, err := files.Edit(t.Context(), "editable/a", DigestSHA256([]byte("old")), []Replacement{{Start: 0, End: 3, Text: []byte("new")}})
			if !errors.Is(err, tt.want) || result != (EditResult{}) {
				t.Fatalf("result=%#v err=%v", result, err)
			}
		})
	}
}

func TestWindowsOperationFilesTreeCanonicalAndReparse(t *testing.T) {
	files, repo := testWindowsOperationFiles(t)
	defer files.Close()
	root := filepath.Join(repo, "editable")
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "z"), []byte("z"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dir", "a"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("dir", filepath.Join(root, "link")); err != nil {
		t.Skipf("Windows reparse fixture unavailable: %v", err)
	}
	want := []byte("d /dir\nf /dir/a\nl /link\nf /z")
	first, err := files.Tree(t.Context(), "editable")
	if err != nil || first.Encoding != "utf-8" || first.Truncated || first.ContentB64 != base64.StdEncoding.EncodeToString(want) || first.EvidenceSHA256 != DigestSHA256(want) {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	second, err := files.Tree(t.Context(), "editable")
	if err != nil || second != first {
		t.Fatalf("second=%#v err=%v", second, err)
	}
	subtree, err := files.Tree(t.Context(), "editable/dir")
	if err != nil || subtree.ContentB64 != base64.StdEncoding.EncodeToString([]byte("f /a")) {
		t.Fatalf("subtree=%#v err=%v", subtree, err)
	}
	if _, err := files.Tree(t.Context(), "editable/link"); !errors.Is(err, ErrOperationUnavailable) {
		t.Fatalf("reparse root=%v", err)
	}
}

func TestWindowsOperationFilesTreeFaultsAndMutation(t *testing.T) {
	for _, tt := range []struct {
		name string
		set  func(*windowsOperationFiles, string)
		want error
	}{
		{"enumerate", func(f *windowsOperationFiles, _ string) {
			f.ops.enumerate = func(windows.Handle) ([]os.DirEntry, error) { return nil, windows.ERROR_READ_FAULT }
		}, ErrOperationUnavailable},
		{"open", func(f *windowsOperationFiles, _ string) {
			f.ops.open = func(windows.Handle, string, bool, uint32) (windows.Handle, error) {
				return 0, windows.ERROR_ACCESS_DENIED
			}
		}, ErrOperationUnavailable},
		{"mutation", func(f *windowsOperationFiles, root string) {
			enumerate := f.ops.enumerate
			mutated := false
			f.ops.enumerate = func(h windows.Handle) ([]os.DirEntry, error) {
				entries, err := enumerate(h)
				if err == nil && !mutated {
					mutated = true
					err = os.WriteFile(filepath.Join(root, "new"), []byte("n"), 0o600)
				}
				return entries, err
			}
		}, ErrOperationConflict},
	} {
		t.Run(tt.name, func(t *testing.T) {
			files, repo := testWindowsOperationFiles(t)
			defer files.Close()
			root := filepath.Join(repo, "editable")
			if err := os.WriteFile(filepath.Join(root, "a"), []byte("a"), 0o600); err != nil {
				t.Fatal(err)
			}
			tt.set(files, root)
			got, err := files.Tree(t.Context(), "editable")
			if !errors.Is(err, tt.want) || got != (InspectResult{}) {
				t.Fatalf("result=%#v err=%v", got, err)
			}
		})
	}
}

func TestWindowsOperationFilesTreeLimitsAndLifecycle(t *testing.T) {
	files, repo := testWindowsOperationFiles(t)
	defer files.Close()
	root := filepath.Join(repo, "editable")
	path := root
	for range maxTreeDepth + 1 {
		path = filepath.Join(path, "d")
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := files.Tree(t.Context(), "editable"); !errors.Is(err, ErrOperationLimit) || got != (InspectResult{}) {
		t.Fatalf("depth=%#v err=%v", got, err)
	}
	if _, err := (*windowsOperationFiles)(nil).Tree(t.Context(), "editable"); !errors.Is(err, ErrOperationUnavailable) {
		t.Fatal(err)
	}
	if _, err := (&windowsOperationFiles{}).Tree(t.Context(), "editable"); !errors.Is(err, ErrOperationUnavailable) {
		t.Fatal(err)
	}
	if err := files.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := files.Tree(t.Context(), "editable"); !errors.Is(err, ErrOperationUnavailable) {
		t.Fatal(err)
	}
}

func editProof(t *testing.T, f *windowsOperationFiles, logical string) windowsDestinationProof {
	t.Helper()
	h, err := f.openFile(logical, windows.FILE_GENERIC_READ|windows.READ_CONTROL)
	if err != nil {
		t.Fatal(err)
	}
	defer f.ops.close(h)
	info, err := f.ops.info(h)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := f.destinationProof(h, info, "")
	if err != nil {
		t.Fatal(err)
	}
	return proof
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
