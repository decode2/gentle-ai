//go:build linux || darwin

package directrun

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
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
