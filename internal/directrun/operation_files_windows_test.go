//go:build windows

package directrun

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
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
		{"partial", "editable/bytes", 1, 2, value[1:3]},
		{"eof", "editable/bytes", 9, 1, nil},
		{"max", "editable/bytes", 0, maxContent, value},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := files.Read(t.Context(), tt.path, tt.offset, tt.limit)
			decoded, decodeErr := base64.StdEncoding.DecodeString(got.ContentB64)
			if err != nil || decodeErr != nil || string(decoded) != string(tt.want) {
				t.Fatalf("read=%#v err=%v decode=%v", got, err, decodeErr)
			}
		})
	}
	for _, path := range []string{"../editable/bytes", "editable/bytes:stream", `editable\\bytes`, "editable/CON", "editable/file.", "editable/file "} {
		if _, err := files.Read(t.Context(), path, 0, 1); !errors.Is(err, ErrOperationInvalidPath) {
			t.Errorf("%q: %v", path, err)
		}
	}
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
